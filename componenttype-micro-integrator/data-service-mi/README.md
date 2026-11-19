# Data Service MI

A REST API that exposes employee data from a MySQL database, built with Choreo Integrator. This sample deploys three components on OpenChoreo: a MySQL database, a one-time database initializer, and the MI data service.

**Source:** [openchoreo/community-modules — data-service-mi](https://github.com/openchoreo/community-modules/tree/main/componenttype-micro-integrator/data-service-mi)

## Endpoints

```
GET  /services/RDBMSDataService/Employee/{EmployeeId}  →  Returns employee by ID
POST /services/RDBMSDataService/Employee               →  Creates a new employee
```

## Prerequisites

- OpenChoreo cluster running with control plane, data plane, and workflow plane installed.
- The `deployment/micro-integrator` component type and `micro-integrator-builder` workflow applied from the parent directory:

```bash
kubectl apply \
  -f ../micro-integrator-build.yaml \
  -f ../micro-integrator-builder.yaml \
  -f ../micro-integrator.yaml
```

## Deploy

### Step 1: Apply MySQL and the database initializer

Apply the MySQL database and the db-init worker using kubectl:

```bash
kubectl apply -f mysql/component.yaml
kubectl apply -f db-init/component.yaml
```

Watch until both pods are running:

```bash
kubectl get pods -n dp-default-default-development-f8e58905 --watch
```

You should see both `mysql-development-*` and `db-init-development-*` reach `1/1 Running`. The db-init pod waits for MySQL to be ready, runs the schema and seed data script, then stays alive.

### Step 2: Deploy the MI data service via Backstage Portal

1. Navigate to your project in the Backstage portal and click **Create Component** from the Project Overview.

2. Choose **Micro Integrator** from the component templates.

3. Complete the required fields in the create form: enter `mi-data-service` as the **Component Name**.

4. Set the deployment source to **Build from Source**, select **wso2-micro-integrator** as the build workflow, then provide the Git repository URL, branch, and application path:
   - Repository URL: `https://github.com/openchoreo/community-modules`
   - Branch: `main`
   - Application path: `./componenttype-micro-integrator/data-service-mi/micro-integrator-ds`

5. Review the provided information and click **Create**.

6. From the component overview page, click **Build**. Once the build succeeds, click **Deploy**.

### Step 3: Set environment variables

Before deploying, configure the following environment variables in the Backstage DevOps portal under **Configs & Secrets**:

| Key | Value |
|---|---|
| `DB_DRIVER_CLASS` | `com.mysql.cj.jdbc.Driver` |
| `DB_CONNECTION_URL` | `jdbc:mysql://mysql:3306/misampledb` |
| `DB_USER` | `misampleuser` |
| `DB_PASS` | `misamplepassword` |

### Step 4: Get the invoke URL

Read the host, path, and port from the ReleaseBinding endpoint status. Use either the `http` or `https` block depending on which scheme you want to invoke.

**HTTP:**

```bash
HOSTNAME=$(kubectl get releasebinding -n default \
  -l openchoreo.dev/component=mi-data-service \
  -o jsonpath='{.items[0].status.endpoints[0].externalURLs.http.host}')

PATH_PREFIX=$(kubectl get releasebinding -n default \
  -l openchoreo.dev/component=mi-data-service \
  -o jsonpath='{.items[0].status.endpoints[0].externalURLs.http.path}')

PORT=$(kubectl get releasebinding -n default \
  -l openchoreo.dev/component=mi-data-service \
  -o jsonpath='{.items[0].status.endpoints[0].externalURLs.http.port}')

echo "Base URL: http://${HOSTNAME}:${PORT}${PATH_PREFIX}"
```

**HTTPS:**

```bash
HOSTNAME=$(kubectl get releasebinding -n default \
  -l openchoreo.dev/component=mi-data-service \
  -o jsonpath='{.items[0].status.endpoints[0].externalURLs.https.host}')

PATH_PREFIX=$(kubectl get releasebinding -n default \
  -l openchoreo.dev/component=mi-data-service \
  -o jsonpath='{.items[0].status.endpoints[0].externalURLs.https.path}')

PORT=$(kubectl get releasebinding -n default \
  -l openchoreo.dev/component=mi-data-service \
  -o jsonpath='{.items[0].status.endpoints[0].externalURLs.https.port}')

echo "Base URL: https://${HOSTNAME}:${PORT}${PATH_PREFIX}"
```

## Try it out

Replace `<BASE_URL>` with the value from the step above, for example:
`http://development-default.openchoreoapis.localhost:8443/mi-data-service-endpoint-1`

### Read an employee

```bash
curl http://<BASE_URL>/services/RDBMSDataService/Employee/14001
```

Expected response:

```xml
<Employees>
  <Employee>
    <EmployeeNumber>14001</EmployeeNumber>
    <FirstName>Will</FirstName>
    <LastName>Smith</LastName>
    <Email>will@google.com</Email>
    <Salary>12000.0</Salary>
  </Employee>
</Employees>
```

### Add a new employee

```bash
curl -X POST http://<BASE_URL>/services/RDBMSDataService/Employee \
  -H "Content-Type: application/xml" \
  -d '<_postemployee>
        <EmployeeNumber>20001</EmployeeNumber>
        <FirstName>Jane</FirstName>
        <LastName>Doe</LastName>
        <Email>jane@example.com</Email>
        <Salary>15000</Salary>
      </_postemployee>'
```

### Verify the new employee was added

```bash
curl http://<BASE_URL>/services/RDBMSDataService/Employee/20001
```

## Seed data

The following employees are pre-loaded by the `db-init` component:

| EmployeeNumber | FirstName | LastName | Email | Salary |
|---|---|---|---|---|
| 14001 | Will | Smith | will@google.com | 12000.0 |
| 14002 | Sam | Rayan | sam@google.com | 1600.0 |
| 14003 | John | Ben | john@google.com | 18500.0 |
| 14004 | Mash | Sean | mash@google.com | 17500.0 |

## Clean up

```bash
kubectl delete component mi-data-service mysql db-init -n default
kubectl delete workload mi-data-service mysql db-init -n default
```
