.PHONY: packaging packaging-build packaging-compose-config packaging-smoke
.PHONY: packaging-measure packaging-validate packaging-up packaging-down
.PHONY: test-vaapi test-browser

IMAGE ?= filetwist:prototype
VERSION ?= $(shell git rev-parse --short=12 HEAD)
REVISION ?= $(shell git rev-parse HEAD)
RENDER_DEVICE ?= /dev/dri/renderD128
COMPOSE = FILETWIST_IMAGE=$(IMAGE) FILETWIST_VERSION=$(VERSION) FILETWIST_REVISION=$(REVISION) docker compose --file deploy/compose.yaml

packaging: packaging-up

packaging-build:
	docker build \
		--platform linux/amd64 \
		--file deploy/Dockerfile \
		--build-arg VERSION=$(VERSION) \
		--build-arg REVISION=$(REVISION) \
		--tag $(IMAGE) .

packaging-compose-config:
	$(COMPOSE) config --quiet
	$(COMPOSE) --file deploy/compose.cpu.yaml config --quiet
	FILETWIST_RENDER_GID=0 $(COMPOSE) --file deploy/compose.vaapi.yaml config --quiet
	FILETWIST_PORT=18080 FILETWIST_CONTAINER_PORT=9443 $(COMPOSE) config --quiet
	FILETWIST_CONTAINER_PORT=9443 $(COMPOSE) config | grep -q 'LISTEN_ADDRESS: .*9443'
	FILETWIST_CONTAINER_PORT=9443 $(COMPOSE) config | grep -q 'target: 9443'
	FILETWIST_PORT= $(COMPOSE) config | grep -q 'host_ip: 127.0.0.1'
	FILETWIST_PORT=0.0.0.0:18080 $(COMPOSE) config | grep -q 'host_ip: 0.0.0.0'

packaging-smoke: packaging-build
	scripts/validate-container.sh $(IMAGE)

packaging-measure: packaging-build
	scripts/measure-container.sh $(IMAGE)

packaging-validate: packaging-compose-config packaging-smoke packaging-measure

packaging-up: packaging-validate
	@if test -c "$(RENDER_DEVICE)"; then \
		render_gid=$$(stat -Lc '%g' "$(RENDER_DEVICE)"); \
		echo "Starting with VA-API device $(RENDER_DEVICE), group $$render_gid"; \
		VAAPI_DEVICE="$(RENDER_DEVICE)" FILETWIST_RENDER_GID=$$render_gid \
			$(COMPOSE) --file deploy/compose.vaapi.yaml up --detach --wait; \
	else \
		echo "Starting in CPU-only mode; $(RENDER_DEVICE) is unavailable"; \
		$(COMPOSE) --file deploy/compose.cpu.yaml up --detach --wait; \
	fi

packaging-down:
	$(COMPOSE) down

test-vaapi:
	ACCELERATION=vaapi go test -v ./internal/media -run '^TestVAAPIIntegration$$'

test-browser:
	FILETWIST_TEST_IMAGE=$(IMAGE) npm --prefix tests/browser test
