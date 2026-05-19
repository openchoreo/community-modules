-- Parse key="value" and key=value pairs from a Ballerina logfmt line.
-- Field names may contain dots and hyphens (e.g. http.status_code_group).
local function parse_logfmt(line)
    local fields = {}
    -- Quoted values first so they take priority over unquoted matches.
    for k, v in line:gmatch('([%w%.%-%_]+)="([^"]*)"') do
        fields[k] = v
    end
    -- Unquoted values (end at first whitespace character).
    for k, v in line:gmatch('([%w%.%-%_]+)=([^%s"]+)') do
        if not fields[k] then
            fields[k] = v
        end
    end
    return fields
end

-- Structural logfmt fields that exist on every line but carry no metric
-- signal — excluded from the metadata payload sent to Moesif.
local EXCLUDE = {
    time=true, level=true, module=true, message=true, logger=true
}

function transform(tag, timestamp, record)
    local log_line = record["log"]
    if not log_line then
        return -1, 0, 0
    end

    -- Trim trailing newline added by Docker JSON log format.
    log_line = log_line:gsub("%s+$", "")

    local f = parse_logfmt(log_line)

    -- Safety check: only process metric log lines.
    if f["logger"] ~= "metrics" then
        return -1, 0, 0
    end

    -- Read app ID from pod annotation for rewrite_tag routing.
    local k8s         = record["kubernetes"] or {}
    local annotations = k8s["annotations"] or {}
    local app_id      = annotations["moesif-app-id"] or ""

    local method = f["http.method"]
    local url    = f["http.url"]

    -- action_name: "METHOD /path" for HTTP services; function name for others
    -- (e.g. FTP listener emits src.function.name="onFileChange").
    local action_name
    if method and method ~= "" and url and url ~= "" then
        action_name = method .. " " .. url
    else
        action_name = f["src.function.name"] or f["entrypoint.function.name"] or "unknown"
    end

    -- request.uri / request.verb: fall back to function-level identifiers when
    -- HTTP fields are absent (e.g. FTP client calls, event handler invocations).
    local req_uri
    if url and url ~= "" then
        req_uri = "https://" .. (f["src.object.name"] or "service") .. url
    else
        local svc = f["entrypoint.service.name"] or f["src.object.name"] or "service"
        local fn  = f["src.function.name"] or "invoke"
        req_uri   = svc .. "/" .. fn
    end

    local req_verb = method or f["src.function.name"] or "INVOKE"

    -- Build metadata dynamically: include every parsed logfmt field whose
    -- value is non-empty. Dots and hyphens in key names are normalised to
    -- underscores for consistent JSON output across HTTP and FTP metrics.
    local metadata = {}
    for k, v in pairs(f) do
        if not EXCLUDE[k] and v ~= "" then
            local norm = k:gsub("[%.%-]", "_")
            if k == "response_time_seconds" then
                metadata[norm] = tonumber(v)
            else
                metadata[norm] = v
            end
        end
    end

    -- moesif_app_id must be at the top level so rewrite_tag can read it
    -- via $moesif_app_id and use it as the new Fluent Bit tag. The tag is
    -- then copied into X-Moesif-Application-Id by header_tag in the output.
    -- record_modifier removes it from the top level before the record is sent.
    local new_record = {
        action_name   = action_name,
        moesif_app_id = app_id,
        request       = {
            time = f["time"],
            uri  = req_uri,
            verb = req_verb,
        },
        metadata = metadata,
    }

    return 1, timestamp, new_record
end
