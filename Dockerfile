# syntax=docker/dockerfile:1
# Static Go binary on distroless, non-root. Phase 0 ships the probe only; Phase 7 adds the
# embedded UI build stage. Multi-arch through buildx: the builder runs natively and cross-compiles.
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/visual-kubernetes ./cmd/visual-kubernetes

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/visual-kubernetes /visual-kubernetes
USER 65532:65532
ENTRYPOINT ["/visual-kubernetes"]
