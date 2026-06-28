ARG GO_IMAGE=golang:1.26
ARG RUNTIME_IMAGE=debian:bookworm-slim

FROM ${GO_IMAGE} AS builder

ARG INSPIREFACE_MODEL=Megatron

WORKDIR /src

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends unzip; \
    rm -rf /var/lib/apt/lists/*

COPY docker-assets/downloads /tmp/inspireface-downloads

RUN set -eux; \
    mkdir -p /tmp/inspireface/extracted /opt/inspireface-sdk /opt/models; \
    sdk_zip="$(find /tmp/inspireface-downloads -maxdepth 1 -type f -name '*.zip' -print -quit)"; \
    model_file="/tmp/inspireface-downloads/${INSPIREFACE_MODEL}"; \
    if [ -z "$sdk_zip" ]; then \
        echo "cannot locate InspireFace SDK zip in docker-assets/downloads" >&2; \
        find /tmp/inspireface-downloads -maxdepth 1 -type f | sort >&2; \
        exit 1; \
    fi; \
    if [ ! -s "$model_file" ]; then \
        echo "cannot locate InspireFace model file: ${INSPIREFACE_MODEL}" >&2; \
        find /tmp/inspireface-downloads -maxdepth 1 -type f | sort >&2; \
        exit 1; \
    fi; \
    unzip -q "$sdk_zip" -d /tmp/inspireface/extracted; \
    include_dir="$(find /tmp/inspireface/extracted -type d -name include -print | while read -r dir; do if [ -f "$dir/inspireface.h" ] || [ -f "$dir/inspireface/inspireface.h" ]; then printf '%s\n' "$dir"; break; fi; done)"; \
    lib_file="$(find /tmp/inspireface/extracted -type f -name 'libInspireFace.so*' -print -quit)"; \
    if [ -z "$include_dir" ] || [ -z "$lib_file" ]; then \
        echo "cannot locate InspireFace include/lib in SDK zip" >&2; \
        find /tmp/inspireface/extracted -maxdepth 5 -type f | sort >&2; \
        exit 1; \
    fi; \
    cp -a "$include_dir" /opt/inspireface-sdk/include; \
    mkdir -p /opt/inspireface-sdk/lib; \
    cp -a "$(dirname "$lib_file")"/. /opt/inspireface-sdk/lib/; \
    cp -a "$model_file" "/opt/models/${INSPIREFACE_MODEL}"; \
    test -s "/opt/models/${INSPIREFACE_MODEL}"; \
    rm -rf /tmp/inspireface /tmp/inspireface-downloads

ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-I/opt/inspireface-sdk/include -I/opt/inspireface-sdk/include/inspireface"
ENV CGO_LDFLAGS="-L/opt/inspireface-sdk/lib -lInspireFace"

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/faced ./cmd/faced

FROM ${RUNTIME_IMAGE} AS runtime

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates; \
    rm -rf /var/lib/apt/lists/*

ENV TZ=Asia/Shanghai
ENV HTTP_ADDR=:5090
ENV DATABASE_URL=postgres://face:face@postgres:5432/face-data?sslmode=disable
ENV LD_LIBRARY_PATH=/opt/inspireface-sdk/lib
ENV INSPIREFACE_PACK_PATH=/opt/models/Megatron

COPY --from=builder /out/faced /usr/local/bin/faced
COPY --from=builder /opt/inspireface-sdk/lib /opt/inspireface-sdk/lib
COPY --from=builder /opt/models/Megatron /opt/models/Megatron

EXPOSE 5090

CMD ["faced"]
