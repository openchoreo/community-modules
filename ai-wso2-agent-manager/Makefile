# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

VENV ?= .venv
PYTHON := $(VENV)/bin/python

.PHONY: unit-test clean

$(PYTHON): scripts/requirements.txt
	python3 -m venv $(VENV)
	$(PYTHON) -m pip install --quiet -r scripts/requirements.txt
	touch $(PYTHON)

unit-test: $(PYTHON)
	$(PYTHON) -m unittest discover -s tests -v

clean:
	rm -rf $(VENV)
