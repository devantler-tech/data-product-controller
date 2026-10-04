# syntax=docker/dockerfile:1.7
#checkov:skip=CKV_DOCKER_2:Kubernetes probes cover each binary in this multi-entrypoint image.
FROM golang:1.26.6-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/manager . && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/demo-product ./cmd/demo-product && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/http-source ./cmd/http-source && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/contract-probe ./cmd/contract-probe && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/dsp-catalog ./cmd/dsp-catalog && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/ui-kit ./cmd/ui-kit && \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/product-check ./cmd/product-check

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/manager /manager
COPY --from=build /out/demo-product /demo-product
COPY --from=build /out/http-source /http-source
COPY --from=build /out/contract-probe /contract-probe
COPY --from=build /out/dsp-catalog /dsp-catalog
COPY --from=build /out/ui-kit /ui-kit
COPY --from=build /out/product-check /product-check
USER 65532:65532
ENTRYPOINT ["/manager"]
