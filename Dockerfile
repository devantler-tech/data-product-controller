# syntax=docker/dockerfile:1.7
FROM golang:1.26.0-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/manager .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/demo-product ./cmd/demo-product

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/manager /manager
COPY --from=build /out/demo-product /demo-product
USER 65532:65532
ENTRYPOINT ["/manager"]
