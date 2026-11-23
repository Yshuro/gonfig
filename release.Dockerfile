# Container image published by GoReleaser (dockers_v2 in .goreleaser.yaml).
# It copies the prebuilt binaries from the build context, so it cannot be built from the sources with docker build.
#
# The binaries are linked against glibc for cgo (Go plugins), so the base provides glibc.
# Once cgo is removed (#244), cgr.dev/chainguard/static suffices.
FROM cgr.dev/chainguard/glibc-dynamic:latest@sha256:7fc93aea6a9305a44f4f10d0cab0c6f5211ab24a2a97320ec75619ab149ee25e

ARG TARGETPLATFORM

COPY $TARGETPLATFORM/gonfig /usr/bin/gonfig
# The plugins for secret managers, in the directory gonfig searches by default. They authenticate on the first
# placeholder using them, so gonfig runs without credentials for secret managers that aren't used.
COPY $TARGETPLATFORM/gonfig-plugin-openbao $TARGETPLATFORM/gonfig-plugin-azure-keyvault /usr/libexec/gonfig/plugins/
COPY LICENSE /usr/share/licenses/gonfig/LICENSE

# The base image runs as the nonroot user (uid 65532)
ENTRYPOINT ["/usr/bin/gonfig"]
