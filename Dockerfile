# syntax=docker/dockerfile:1
# Static Go binary with the page embedded, on distroless, non-root. Multi-arch through buildx:
# both build stages run on the builder's own platform and the Go stage cross-compiles, so
# `docker buildx build --platform linux/amd64,linux/arm64` needs no emulation.
FROM --platform=$BUILDPLATFORM node:26-slim AS web
WORKDIR /src/web
# corepack is pinned (node 26 no longer bundles it); it then fetches the pnpm version that
# web/package.json's packageManager field names.
RUN npm install -g corepack@0.36.0 && corepack enable pnpm
COPY web/package.json web/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
# vite writes the client build to ../cmd/visual-kubernetes/ui/dist (web/vite.config.ts); the
# typecheck (`tsc -b`) runs in the dev loop, not here, because it also reads test/e2e. The SPA
# prerender fetches its own preview server on 127.0.0.1; ipv4first makes `localhost` (where
# that server listens) resolve there too, as it does on macOS.
RUN NODE_OPTIONS=--dns-result-order=ipv4first pnpm exec vite build

FROM --platform=$BUILDPLATFORM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY --from=web /src/cmd/visual-kubernetes/ui/dist cmd/visual-kubernetes/ui/dist
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/visual-kubernetes ./cmd/visual-kubernetes

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/visual-kubernetes /visual-kubernetes
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/visual-kubernetes"]
