# syntax=docker/dockerfile:1.7
FROM golang:1.26.6-alpine AS build
WORKDIR /src
COPY tests/source/fixture/*.go ./
RUN CGO_ENABLED=0 GO111MODULE=off GOFLAGS=-p=1 go build -trimpath -ldflags="-s -w" -o /fixture .

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /fixture /fixture
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=5s CMD ["/fixture", "probe", "--url", "http://127.0.0.1:9000/healthz", "--timeout", "3s"]
ENTRYPOINT ["/fixture"]
CMD ["serve"]
