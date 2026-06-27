ARG INSPIREFACE_SDK_IMAGE=inspireface-cgo-check:latest

FROM ${INSPIREFACE_SDK_IMAGE} AS inspireface-sdk

FROM golang:1.26 AS builder

WORKDIR /src

COPY --from=inspireface-sdk /opt/inspireface-sdk /opt/inspireface-sdk

ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-I/opt/inspireface-sdk/include -I/opt/inspireface-sdk/include/inspireface"
ENV CGO_LDFLAGS="-L/opt/inspireface-sdk/lib -lInspireFace"

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /out/faced ./cmd/faced && \
    go build -o /out/facecli ./cmd/facecli

FROM ubuntu:22.04 AS runtime

ENV TZ=Asia/Shanghai
ENV HTTP_ADDR=:5090
ENV LD_LIBRARY_PATH=/opt/inspireface-sdk/lib
ENV INSPIREFACE_PACK_PATH=/opt/models/Megatron

COPY --from=builder /out/faced /usr/local/bin/faced
COPY --from=builder /out/facecli /usr/local/bin/facecli
COPY --from=inspireface-sdk /opt/inspireface-sdk/lib /opt/inspireface-sdk/lib
COPY --from=inspireface-sdk /opt/models/Megatron /opt/models/Megatron

EXPOSE 5090

CMD ["faced"]
