# The TokenOps team plane (cmd/tokenops-team), built from this repository.
# Build context is the repository root: see docker-compose.build.yml. The
# release publishes an image built from its archives instead (Dockerfile.release).
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X go.klarlabs.de/tokenops/internal/version.Version=${VERSION}" \
    -o /out/tokenops-team ./cmd/tokenops-team

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/tokenops-team /usr/local/bin/tokenops-team
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/tokenops-team"]
CMD ["serve"]
