# Build only the relay. The fixed vendored modules preserve go.mod replacements;
# this target does not embed or download Cage virtual-machine images.
FROM golang:1.26.10-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party ./third_party
COPY internal/relay ./internal/relay
COPY cmd/cage-relay ./cmd/cage-relay
RUN CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/cage-relay ./cmd/cage-relay

FROM scratch
COPY --from=build /out/cage-relay /cage-relay
USER 65532:65532
EXPOSE 8787
ENTRYPOINT ["/cage-relay"]
