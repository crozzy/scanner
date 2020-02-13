SHELL=/bin/bash
BASE_DIR=$(CURDIR)

TAG=$(shell ./get-tag)

FORMATTING_FILES=$(shell git grep -L '^// Code generated by .* DO NOT EDIT\.' -- '*.go')

#####################################################################
###### Binaries we depend on (need to be defined on top) ############
#####################################################################

STATICCHECK_BIN := $(GOPATH)/bin/staticcheck
$(STATICCHECK_BIN): deps
	@echo "+ $@"
	@go install honnef.co/go/tools/cmd/staticcheck

GOIMPORTS_BIN := $(GOPATH)/bin/goimports
$(GOIMPORTS_BIN): deps
	@echo "+ $@"
	go install golang.org/x/tools/cmd/goimports

GOLINT_BIN := $(GOPATH)/bin/golint
$(GOLINT_BIN): deps
	@echo "+ $@"
	go install golang.org/x/lint/golint

EASYJSON_BIN := $(GOPATH)/bin/easyjson
$(EASYJSON_BIN): deps
	@echo "+ $@"
	go install github.com/mailru/easyjson/easyjson

#############
##  Build  ##
#############
.PHONY: build-updater
build-updater: deps
	@echo "+ $@"
	go build -o ./bin/updater ./cmd/updater

###########
## Style ##
###########
.PHONY: style
style: imports blanks fmt lint vet staticcheck no-large-files

.PHONY: staticcheck
staticcheck: $(STATICCHECK_BIN)
	@echo "+ $@"
	@$(BASE_DIR)/tools/staticcheck-wrap.sh ./...

.PHONY: fmt
fmt:
	@echo "+ $@"
ifdef CI
		@echo "The environment indicates we are in CI; checking gofmt."
		@echo 'If this fails, run `make style`.'
		@$(eval FMT=`echo $(FORMATTING_FILES) | xargs gofmt -s -l`)
		@echo "gofmt problems in the following files, if any:"
		@echo $(FMT)
		@test -z "$(FMT)"
endif
	@echo $(FORMATTING_FILES) | xargs gofmt -s -l -w

.PHONY: imports
imports: deps $(GOIMPORTS_BIN)
	@echo "+ $@"
ifdef CI
		@echo "The environment indicates we are in CI; checking goimports."
		@echo 'If this fails, run `make style`.'
		@$(eval IMPORTS=`echo $(FORMATTING_FILES) | xargs goimports -l`)
		@echo "goimports problems in the following files, if any:"
		@echo $(IMPORTS)
		@test -z "$(IMPORTS)"
endif
	@echo $(FORMATTING_FILES) | xargs goimports -w

.PHONY: no-large-files
no-large-files:
	@echo "+ $@"
	@$(BASE_DIR)/tools/large-git-files/find.sh

.PHONY: lint
lint: $(GOLINT_BIN)
	@echo "+ $@"
	@$(BASE_DIR)/tools/go-lint.sh $(FORMATTING_FILES)

.PHONY: vet
vet:
	@echo "+ $@"
	@$(BASE_DIR)/tools/go-vet.sh -tags "$(subst $(comma),$(space),$(RELEASE_GOTAGS))" $(shell go list -e ./... | grep -v generated | grep -v vendor)

.PHONY: blanks
blanks:
	@echo "+ $@"
ifdef CI
	@echo $(FORMATTING_FILES) | xargs $(BASE_DIR)/tools/import_validate.py
else
	@echo $(FORMATTING_FILES) | xargs $(BASE_DIR)/tools/fix-blanks.sh
endif

.PHONY: dev
dev: install-dev-tools
	@echo "+ $@"

deps: proto-generated-srcs go.mod
	@echo "+ $@"
	@go mod tidy
	@$(MAKE) download-deps
ifdef CI
	@git diff --exit-code -- go.mod go.sum || { echo "go.mod/go.sum files were updated after running 'go mod tidy', run this command on your local machine and commit the results." ; exit 1 ; }
	go mod verify
endif
	@touch deps

.PHONY: download-deps
download-deps:
	@echo "+ $@"
	@go mod download

.PHONY: clean-deps
clean-deps:
	@echo "+ $@"
	@rm -f deps

GET_DEVTOOLS_CMD := $(MAKE) -qp | sed -e '/^\# Not a target:$$/{ N; d; }' | egrep -v '^(\s*(\#.*)?$$|\s|%|\(|\.)' | egrep '^[^[:space:]:]*:' | cut -d: -f1 | sort | uniq | grep '^$(GOPATH)/bin/'
.PHONY: clean-dev-tools
clean-dev-tools:
	@echo "+ $@"
	@$(GET_DEVTOOLS_CMD) | xargs rm -fv

.PHONY: reinstall-dev-tools
reinstall-dev-tools: clean-dev-tools
	@echo "+ $@"
	@$(MAKE) install-dev-tools

.PHONY: install-dev-tools
install-dev-tools:
	@echo "+ $@"
	@$(GET_DEVTOOLS_CMD) | xargs $(MAKE)

############
## Images ##
############

.PHONY: image
image: scanner-image db-image

.PHONY: image-rhel
image-rhel: scanner-image-rhel db-image-rhel

.PHONY: build
build: deps
	@echo "+ $@"
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o image/scanner/bin/scanner ./cmd/clair

.PHONY: scanner-image
scanner-image: build
	@echo "+ $@"
	@docker build -t us.gcr.io/stackrox-ci/scanner:$(TAG) -f image/scanner/alpine/Dockerfile image/scanner
	@docker tag us.gcr.io/stackrox-ci/scanner:$(TAG) stackrox/scanner:$(TAG)

.PHONY: $(CURDIR)/image/scanner/rhel/bundle.tar.gz
$(CURDIR)/image/scanner/rhel/bundle.tar.gz: build
	$(CURDIR)/image/scanner/rhel/create-bundle.sh $(CURDIR)/image/scanner $@

.PHONY: $(CURDIR)/image/db/rhel/bundle.tar.gz
$(CURDIR)/image/db/rhel/bundle.tar.gz:
	$(CURDIR)/image/db/rhel/create-bundle.sh $(CURDIR)/image/db $@

.PHONY: scanner-image-rhel
scanner-image-rhel: build $(CURDIR)/image/scanner/rhel/bundle.tar.gz
	@echo "+ $@"
	@docker build -t us.gcr.io/stackrox-ci/scanner-rhel:$(TAG) -f image/scanner/rhel/Dockerfile image/scanner/rhel

.PHONY: db-image
db-image:
	@echo "+ $@"
	@test -f image/db/dump/definitions.sql.gz || { echo "FATAL: No definitions dump found in image/dump/definitions.sql.gz. Exiting..."; exit 1; }
	@docker build -t us.gcr.io/stackrox-ci/scanner-db:$(TAG) -f image/db/alpine/Dockerfile image/db
	@docker tag us.gcr.io/stackrox-ci/scanner-db:$(TAG) stackrox/scanner-db:$(TAG)


.PHONY: db-image-rhel
db-image-rhel: $(CURDIR)/image/db/rhel/bundle.tar.gz
	@echo "+ $@"
	@test -f image/db/dump/definitions.sql.gz || { echo "FATAL: No definitions dump found in image/dump/definitions.sql.gz. Exiting..."; exit 1; }
	@docker build -t us.gcr.io/stackrox-ci/scanner-db-rhel:$(TAG) -f image/db/rhel/Dockerfile image/db/rhel

.PHONY: deploy
deploy: clean-helm-rendered
	@echo "+ $@"
	kubectl create namespace stackrox || true
	helm template chart/ --name scanner --set tag=$(TAG),logLevel=DEBUG,updateInterval=2m --output-dir rendered-chart
	kubectl apply -R -f rendered-chart

.PHONY: deploy-dockerhub
deploy-dockerhub: clean-helm-rendered
	@echo "+ $@"
	kubectl create namespace stackrox || true
	helm template chart/ --name scanner --set tag=$(TAG),logLevel=DEBUG,updateInterval=2m,scannerImage=stackrox/scanner,scannerDBImage=stackrox/scanner-db --output-dir rendered-chart
	kubectl apply -R -f rendered-chart


.PHONY: deploy-rhel
deploy-rhel: clean-helm-rendered
	@echo "+ $@"
	kubectl create namespace stackrox || true
	helm template chart/ --name scanner --set tag=$(TAG),logLevel=DEBUG,updateInterval=2m,scannerImage="us.gcr.io/stackrox-ci/scanner-rhel",scannerDBImage="us.gcr.io/stackrox-ci/scanner-db-rhel" --output-dir rendered-chart
	kubectl apply -R -f rendered-chart

###########
## Tests ##
###########

.PHONY: unit-tests
unit-tests: deps
	@echo "+ $@"
	go test -race ./...

.PHONY: e2e-tests
e2e-tests: deps
	@echo "+ $@"
	go test -tags e2e -count=1 ./e2etests/...

.PHONY: no-license-tests
no-license-tests: deps
	@echo "+ $@"
	go test -tags e2e_nolicense -count=1 ./e2etests/...

####################
## Generated Srcs ##
####################

PROTO_GENERATED_SRCS = $(GENERATED_PB_SRCS) $(GENERATED_API_GW_SRCS)

include make/protogen.mk

proto-generated-srcs: $(PROTO_GENERATED_SRCS)
	@echo "+ $@"
	@touch proto-generated-srcs

.PHONY: go-easyjson-srcs
go-easyjson-srcs: $(EASYJSON_BIN)
	@echo "+ $@"
	@easyjson -pkg pkg/nvdloader

clean-proto-generated-srcs:
	@echo "+ $@"
	git clean -xdf generated

###########
## Clean ##
###########
.PHONY: clean
clean: clean-image clean-helm-rendered
	@echo "+ $@"

.PHONY: clean-image
clean-image:
	@echo "+ $@"
	git clean -xdf image/bin

.PHONY: clean-helm-rendered
clean-helm-rendered:
	@echo "+ $@"
	git clean -xdf rendered-chart
