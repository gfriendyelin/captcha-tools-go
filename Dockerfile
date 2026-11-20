# syntax=docker/dockerfile:1.19
FROM debian:bookworm-slim AS libjpeg
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl  && rm -rf /var/lib/apt/lists/*
RUN set -eux;  case "$TARGETARCH" in   amd64) JPEG_URL="https://github.com/libjpeg-turbo/libjpeg-turbo/releases/download/3.1.4.1/libjpeg-turbo-official_3.1.4.1_amd64.deb"; JPEG_SHA256="c0b8cf74ba68a083008800804c2146a2eb7740d3e7506d6a3fd93a169248ce21" ;;   arm64) JPEG_URL="https://github.com/libjpeg-turbo/libjpeg-turbo/releases/download/3.1.4.1/libjpeg-turbo-official_3.1.4.1_arm64.deb"; JPEG_SHA256="d3f9d7ddd998758e1850b37153a7904bdcdeb8cf54a782c772780b0bc50dd553" ;;   *) echo "unsupported TARGETARCH=$TARGETARCH" >&2; exit 1 ;;  esac;  curl --fail --location --retry 5 --output /tmp/libjpeg.deb "$JPEG_URL";  echo "$JPEG_SHA256  /tmp/libjpeg.deb" | sha256sum -c -;  mkdir -p /out;  dpkg-deb -x /tmp/libjpeg.deb /out;  rm -f /tmp/libjpeg.deb

FROM golang:1.27.4-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=libjpeg /out/opt/libjpeg-turbo /opt/libjpeg-turbo
ARG TARGETOS=linux
ARG TARGETARCH
RUN mkdir -p /out
RUN CGO_ENABLED=1 GOOS=$TARGETOS GOARCH=$TARGETARCH     CGO_CFLAGS="-I/opt/libjpeg-turbo/include"     CGO_LDFLAGS="-L/opt/libjpeg-turbo/lib64"     go build -trimpath -ldflags="-s -w -buildid=" -o /out/captcha-tools ./cmd/captcha-tools

FROM build AS test
ENV LD_LIBRARY_PATH=/opt/libjpeg-turbo/lib64
RUN go test -count=1 ./...

FROM debian:bookworm-slim AS onnxruntime
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl unzip  && rm -rf /var/lib/apt/lists/*
RUN set -eux;  case "$TARGETARCH" in   amd64) ORT_URL="https://files.pythonhosted.org/packages/6c/44/1e9e762b95b7da0a8424913a1ed7c38cdaf88624a3c41ddba24ebac88bc9/onnxruntime-1.31.0-cp312-cp312-manylinux_2_28_x86_64.whl"; ORT_SHA256="5e129d6c56abd53e659cb70f00a108d6824086470ff99c2e47a82e5786563db3" ;;   arm64) ORT_URL="https://files.pythonhosted.org/packages/53/1a/561b43ca1536d9e81d1785bb8a1a260a9e314ef6d04976ba0411c652bda1/onnxruntime-1.31.0-cp312-cp312-manylinux_2_28_aarch64.whl"; ORT_SHA256="35758d7606d578ec5b9d65f6e8a1f488013194c3f6097038a3223cb26d35ef9a" ;;   *) echo "unsupported TARGETARCH=$TARGETARCH" >&2; exit 1 ;;  esac;  curl --fail --location --retry 5 --output /tmp/onnxruntime.whl "$ORT_URL";  echo "$ORT_SHA256  /tmp/onnxruntime.whl" | sha256sum -c -;  mkdir -p /out;  unzip -j /tmp/onnxruntime.whl 'onnxruntime/capi/libonnxruntime*.so*' -d /out;  rm -f /tmp/onnxruntime.whl

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends     ca-certificates tzdata libstdc++6 libgcc-s1 libgomp1  && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*  && groupadd --gid 10001 captcha  && useradd --uid 10001 --gid captcha --no-create-home --home-dir /app captcha
WORKDIR /app
COPY --from=onnxruntime /out/ /usr/lib/
COPY --from=libjpeg /out/opt/libjpeg-turbo/lib64/libjpeg.so* /opt/libjpeg-turbo/lib64/
COPY --from=libjpeg /out/opt/libjpeg-turbo/share/doc /opt/libjpeg-turbo/share/doc
RUN echo '/opt/libjpeg-turbo/lib64' > /etc/ld.so.conf.d/00-libjpeg-turbo.conf && ldconfig
COPY --from=build /out/captcha-tools /app/captcha-tools
COPY model/ /app/model/
RUN mkdir -p /app/img_2_val /app/img_saved /app/logs  && chown -R captcha:captcha /app
ENV use_pdl=1 use_dfine=1 use_multi=1     MODEL_PRELOAD=false MODEL_IDLE_TIMEOUT=10m LOG_LEVEL=INFO     DINO_RANDOM_OFFSET=1     ONNXRUNTIME_SHARED_LIBRARY_PATH=/usr/lib/libonnxruntime.so.1.31.0
USER captcha
EXPOSE 9645
VOLUME ["/app/img_saved", "/app/logs"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3  CMD ["/app/captcha-tools", "-healthcheck"]
ENTRYPOINT ["/app/captcha-tools"]
