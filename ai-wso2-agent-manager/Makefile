# Copyright 2026 The OpenChoreo Authors
# SPDX-License-Identifier: Apache-2.0

VENV ?= .venv
PYTHON := $(VENV)/bin/python
# A stamp file, not $(PYTHON): the venv's python is a symlink to the system
# interpreter, which a non-root user cannot touch.
INSTALLED := $(VENV)/.requirements-installed

.PHONY: unit-test clean

$(INSTALLED): scripts/requirements.txt
	python3 -m venv $(VENV)
	$(PYTHON) -m pip install --quiet -r scripts/requirements.txt
	touch $(INSTALLED)

unit-test: $(INSTALLED)
	$(PYTHON) -m unittest discover -s tests -v

clean:
	rm -rf $(VENV)
