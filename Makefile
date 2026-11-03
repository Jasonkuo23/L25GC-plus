GO_BIN_PATH = bin
GO_SRC_PATH = NFs
C_BUILD_PATH = build
ROOT_PATH = $(shell pwd)
OPENAPI_OVERLAY = $(C_BUILD_PATH)/openapi-oauth-overlay.json
N2_TRANSPORT_PATH = lib/n2-transport
N2_SCTP_LIB = $(C_BUILD_PATH)/n2-usrsctp/install/lib/libusrsctp.a
N2_TRANSPORT_FILES = $(wildcard $(N2_TRANSPORT_PATH)/*.go $(N2_TRANSPORT_PATH)/*.c $(N2_TRANSPORT_PATH)/*.h $(N2_TRANSPORT_PATH)/go.mod)

NF = $(GO_NF)
GO_NF = amf ausf nrf nssf pcf smf udm udr chf n3iwf
PATCHED_GO_NF = amf ausf nrf nssf pcf smf udm udr chf

NF_GO_FILES = $(shell find $(GO_SRC_PATH)/$(%) -name "*.go" ! -name "*_test.go")

# VERSION = $(shell git describe --tags)
# BUILD_TIME = $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
# COMMIT_HASH = $(shell git submodule status | grep $(GO_SRC_PATH)/$(@F) | awk '{print $$(1)}' | cut -c1-8)
# COMMIT_TIME = $(shell cd $(GO_SRC_PATH)/$(@F) && git log --pretty="@%at" -1 | xargs date -u +"%Y-%m-%dT%H:%M:%SZ" -d)
# LDFLAGS = -X github.com/free5gc/util/version.VERSION=$(VERSION) \
#           -X github.com/free5gc/util/version.BUILD_TIME=$(BUILD_TIME) \
#           -X github.com/free5gc/util/version.COMMIT_HASH=$(COMMIT_HASH) \
#           -X github.com/free5gc/util/version.COMMIT_TIME=$(COMMIT_TIME)

.PHONY: $(NF) clean n2-usrsctp

.DEFAULT_GOAL: nfs

nfs: $(NF)

all: $(NF)

debug: GCFLAGS += -N -l
debug: all

$(GO_NF): % : $(GO_BIN_PATH)/%

n2-usrsctp: $(N2_SCTP_LIB)

$(N2_SCTP_LIB): scripts/build/n2_usrsctp.py $(N2_TRANSPORT_PATH)/native/ip_adapter.inc
	python3 scripts/build/n2_usrsctp.py $(if $(USRSCTP_SOURCE),--source "$(USRSCTP_SOURCE)")

$(OPENAPI_OVERLAY): patches/openapi/oauth/get_token_context.go patches/net/http2/onvm_server.go scripts/build/openapi_oauth_overlay.py
	python3 scripts/build/openapi_oauth_overlay.py --output $@

$(addprefix $(GO_BIN_PATH)/,$(PATCHED_GO_NF)): $(OPENAPI_OVERLAY)

$(GO_BIN_PATH)/amf $(GO_BIN_PATH)/n3iwf: export CGO_CFLAGS_ALLOW = -mrtm
$(GO_BIN_PATH)/amf $(GO_BIN_PATH)/n3iwf: $(N2_TRANSPORT_FILES) $(N2_SCTP_LIB)
$(GO_BIN_PATH)/amf: $(shell find $(GO_SRC_PATH)/amf -name '*.go' ! -name '*_test.go') $(GO_SRC_PATH)/amf/go.mod $(GO_SRC_PATH)/amf/go.sum
$(GO_BIN_PATH)/n3iwf: $(shell find $(GO_SRC_PATH)/n3iwf -name '*.go' ! -name '*_test.go') $(GO_SRC_PATH)/n3iwf/go.mod $(GO_SRC_PATH)/n3iwf/go.sum
$(GO_BIN_PATH)/n3iwf: $(wildcard $(GO_SRC_PATH)/n3iwf-up-client/*.go $(GO_SRC_PATH)/n3iwf-up-client/*.c $(GO_SRC_PATH)/n3iwf-up-client/*.h $(GO_SRC_PATH)/n3iwf-up-client/go.mod)

$(GO_BIN_PATH)/%: $(NF_GO_FILES)
# $(@F): The file-within-directory part of the file name of the target.
	@echo "Start building $(@F)...."
	cd $(GO_SRC_PATH)/$(@F)/cmd && \
	CGO_ENABLED=1 go build $(if $(filter $(@F),$(PATCHED_GO_NF)),-overlay=$(ROOT_PATH)/$(OPENAPI_OVERLAY)) -gcflags "$(GCFLAGS)" -ldflags "$(LDFLAGS)" -o $(ROOT_PATH)/$@ main.go

vpath %.go $(addprefix $(GO_SRC_PATH)/, $(GO_NF))

clean:
	rm -rf $(addprefix $(GO_BIN_PATH)/, $(GO_NF))
