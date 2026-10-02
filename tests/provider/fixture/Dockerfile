# syntax=docker/dockerfile:1.7
FROM golang:1.26.6-alpine AS build
WORKDIR /src
COPY tests/provider/fixture/go.mod tests/provider/fixture/go.sum ./
RUN go mod download
COPY tests/provider/fixture/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /fixture .

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /fixture /fixture
USER 65532:65532
HEALTHCHECK --interval=30s --timeout=10s CMD ["/fixture", "probe"]
ENTRYPOINT ["/fixture"]
CMD ["serve"]
