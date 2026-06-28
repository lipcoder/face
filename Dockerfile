ARG GO_IMAGE=golang:1.26
ARG RUNTIME_IMAGE=debian:bookworm-slim

# 构建阶段，使用 Go 官方镜像进行编译
FROM ${GO_IMAGE} AS builder

ARG TARGETARCH
ARG INSPIREFACE_VERSION=v1.2.3
ARG INSPIREFACE_REPO=HyperInspire/InspireFace
ARG INSPIREFACE_MODEL=Megatron

WORKDIR /src

# 修改 APT 源为清华源，安装依赖包
RUN set -eux; \
    . /etc/os-release; \
    write_debian_sources() { \
        debian_mirror="$1"; \
        security_mirror="$2"; \
        rm -f /etc/apt/sources.list.d/debian.sources; \
        { \
            echo "deb ${debian_mirror} ${VERSION_CODENAME} main"; \
            echo "deb ${debian_mirror} ${VERSION_CODENAME}-updates main"; \
            echo "deb ${security_mirror} ${VERSION_CODENAME}-security main"; \
        } > /etc/apt/sources.list; \
    }; \
    install_apt_deps() { \
        apt-get -o Acquire::Retries=3 update && \
        apt-get -o Acquire::Retries=3 install -y --no-install-recommends ca-certificates curl unzip; \
    }; \
    write_debian_sources http://mirrors.tuna.tsinghua.edu.cn/debian http://mirrors.tuna.tsinghua.edu.cn/debian-security; \
    install_apt_deps || { \
        write_debian_sources https://mirrors.tuna.tsinghua.edu.cn/debian https://mirrors.tuna.tsinghua.edu.cn/debian-security; \
        install_apt_deps; \
    } || { \
        write_debian_sources http://mirrors.aliyun.com/debian http://mirrors.aliyun.com/debian-security; \
        install_apt_deps; \
    } || { \
        write_debian_sources http://deb.debian.org/debian http://security.debian.org/debian-security; \
        install_apt_deps; \
    }; \
    rm -rf /var/lib/apt/lists/*

# 下载并安装 InspireFace SDK 和模型文件
RUN set -eux; \
    targetarch="${TARGETARCH:-$(dpkg --print-architecture)}"; \
    sdk_version="${INSPIREFACE_VERSION#v}"; \
    case "$targetarch" in \
        amd64) sdk_asset="inspireface-linux-x86-manylinux2014-${sdk_version}.zip" ;; \
        arm64) sdk_asset="inspireface-linux-aarch64-${sdk_version}.zip" ;; \
        *) echo "unsupported target architecture: ${targetarch}" >&2; exit 1 ;; \
    esac; \
    mkdir -p /tmp/inspireface /opt/inspireface-sdk /opt/models; \
    curl -fL "https://github.com/${INSPIREFACE_REPO}/releases/download/${INSPIREFACE_VERSION}/${sdk_asset}" -o /tmp/inspireface/sdk.zip; \
    unzip -q /tmp/inspireface/sdk.zip -d /tmp/inspireface/extracted; \
    include_dir="$(find /tmp/inspireface/extracted -type d -name include -print | while read -r dir; do if [ -f "$dir/inspireface.h" ] || [ -f "$dir/inspireface/inspireface.h" ]; then printf '%s\n' "$dir"; break; fi; done)"; \
    lib_file="$(find /tmp/inspireface/extracted -type f -name 'libInspireFace.so*' -print -quit)"; \
    if [ -z "$include_dir" ] || [ -z "$lib_file" ]; then \
        echo "cannot locate InspireFace include/lib in ${sdk_asset}" >&2; \
        find /tmp/inspireface/extracted -maxdepth 4 -type f | sort >&2; \
        exit 1; \
    fi; \
    cp -a "$include_dir" /opt/inspireface-sdk/include; \
    mkdir -p /opt/inspireface-sdk/lib; \
    cp -a "$(dirname "$lib_file")"/. /opt/inspireface-sdk/lib/; \
    curl -fL "https://github.com/${INSPIREFACE_REPO}/releases/download/v1.x/${INSPIREFACE_MODEL}" -o "/opt/models/${INSPIREFACE_MODEL}"; \
    test -s "/opt/models/${INSPIREFACE_MODEL}"; \
    rm -rf /tmp/inspireface

ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-I/opt/inspireface-sdk/include -I/opt/inspireface-sdk/include/inspireface"
ENV CGO_LDFLAGS="-L/opt/inspireface-sdk/lib -lInspireFace"

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath -ldflags="-s -w" -o /out/faced ./cmd/faced

FROM ${RUNTIME_IMAGE} AS runtime

# 修改 APT 源为清华源，安装 ca-certificates
RUN set -eux; \
    . /etc/os-release; \
    write_debian_sources() { \
        debian_mirror="$1"; \
        security_mirror="$2"; \
        rm -f /etc/apt/sources.list.d/debian.sources; \
        { \
            echo "deb ${debian_mirror} ${VERSION_CODENAME} main"; \
            echo "deb ${debian_mirror} ${VERSION_CODENAME}-updates main"; \
            echo "deb ${security_mirror} ${VERSION_CODENAME}-security main"; \
        } > /etc/apt/sources.list; \
    }; \
    install_apt_deps() { \
        apt-get -o Acquire::Retries=3 update && \
        apt-get -o Acquire::Retries=3 install -y --no-install-recommends ca-certificates; \
    }; \
    write_debian_sources http://mirrors.tuna.tsinghua.edu.cn/debian http://mirrors.tuna.tsinghua.edu.cn/debian-security; \
    install_apt_deps || { \
        write_debian_sources https://mirrors.tuna.tsinghua.edu.cn/debian https://mirrors.tuna.tsinghua.edu.cn/debian-security; \
        install_apt_deps; \
    } || { \
        write_debian_sources http://mirrors.aliyun.com/debian http://mirrors.aliyun.com/debian-security; \
        install_apt_deps; \
    } || { \
        write_debian_sources http://deb.debian.org/debian http://security.debian.org/debian-security; \
        install_apt_deps; \
    }; \
    rm -rf /var/lib/apt/lists/*

ENV TZ=Asia/Shanghai
ENV HTTP_ADDR=:5090
ENV DATABASE_URL=postgres://face:face@postgres:5432/face-data?sslmode=disable
ENV LD_LIBRARY_PATH=/opt/inspireface-sdk/lib
ENV INSPIREFACE_PACK_PATH=/opt/models/Megatron

# 将编译好的二进制文件和依赖的 SDK、模型文件从 builder 阶段复制到 runtime 阶段
COPY --from=builder /out/faced /usr/local/bin/faced
COPY --from=builder /opt/inspireface-sdk/lib /opt/inspireface-sdk/lib
COPY --from=builder /opt/models/Megatron /opt/models/Megatron

EXPOSE 5090

CMD ["faced"]
