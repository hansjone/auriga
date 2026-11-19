# NODE_IMAGE must be the reviewed node:22.19.0-bookworm-slim manifest digest.
# Build with scripts/build-real-dsh.mjs; tags alone are rejected there.
ARG NODE_IMAGE
FROM ${NODE_IMAGE} AS toolchain
# Match the workspace's reviewed package-manager version. Corepack verifies its distribution.
ENV COREPACK_HOME=/opt/corepack COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable pnpm && corepack prepare pnpm@11.28.6 --activate
ENV COREPACK_ENABLE_NETWORK=0

FROM ${NODE_IMAGE} AS runtime-tooling
WORKDIR /opt/auriga-tools/pnpm
COPY deploy/compose/real-dsh/tooling/package*.json ./
COPY deploy/compose/real-dsh/tooling.mjs /opt/fixture/tooling.mjs
COPY clients/installer/upstream.json /opt/fixture/runtime-pins.json
COPY runtime-tooling/pnpm.tgz /opt/runtime-input/pnpm.tgz
# The original approved tarball is supplied by the matching offline devkit.
# No Corepack/PATH lookup, registry fallback or lifecycle scripts install tools.
RUN --network=none node /opt/fixture/tooling.mjs input /opt/runtime-input/pnpm.tgz \
    && npm cache add /opt/runtime-input/pnpm.tgz --offline --ignore-scripts --cache /tmp/runtime-tool-cache \
    && npm ci --offline --ignore-scripts --bin-links=false --no-audit --no-fund --cache /tmp/runtime-tool-cache \
    && node /opt/fixture/tooling.mjs installed

FROM toolchain AS broker
WORKDIR /build
COPY package.json pnpm-workspace.yaml ./
COPY web/pnpm-lock.yaml ./web/
COPY clients/dsh-plugin/package.json ./clients/dsh-plugin/
RUN CI=true pnpm --filter auriga --filter auriga-dsh install --frozen-lockfile --ignore-scripts
RUN --network=none --mount=type=bind,target=/source \
    node /source/deploy/compose/real-dsh/broker-inputs.mjs /source /build /broker-package/broker-source.sha256 \
    && node clients/shell-auth/build.mjs /broker-artifact --managed-linux \
    && node clients/dsh-plugin/scripts/package-broker.mjs /broker-artifact --managed-linux \
    && mv clients/dsh-plugin/shell-auth /broker-package/shell-auth

FROM ${NODE_IMAGE} AS build
WORKDIR /opt/dsh
COPY deploy/compose/real-dsh/package*.json ./
COPY deploy/compose/real-dsh/versions.json /opt/fixture/versions.json
COPY deploy/compose/real-dsh/build.mjs deploy/compose/real-dsh/inputs.mjs deploy/compose/real-dsh/runtime.mjs deploy/compose/real-dsh/start.mjs deploy/compose/real-dsh/tooling.mjs /opt/fixture/
COPY clients/installer/upstream.json /opt/fixture/runtime-pins.json
COPY --from=runtime-tooling /opt/auriga-tools /opt/auriga-tools
COPY clients/dsh-plugin/package.json /opt/fixture/plugin-package.json
RUN node /opt/fixture/build.mjs check
RUN npm ci --omit=dev --include=optional --ignore-scripts --bin-links=false --no-audit --no-fund --registry=https://registry.npmjs.org --os=linux --cpu=x64 --libc=glibc
WORKDIR /opt/auriga-dsh
COPY clients/dsh-plugin/package*.json ./
RUN node /opt/fixture/build.mjs plugin-lock
RUN npm ci --omit=dev --include=optional --ignore-scripts --bin-links=false --no-audit --no-fund --registry=https://registry.npmjs.org
COPY clients/dsh-plugin/src ./src
COPY clients/dsh-plugin/LICENSE ./LICENSE
COPY clients/dsh-plugin/cordis.patch.yml ./cordis.patch.yml
COPY --from=broker /broker-package/shell-auth ./shell-auth
COPY --from=broker /broker-package/broker-source.sha256 /opt/fixture/
RUN ln -s /opt/auriga-dsh /opt/dsh/node_modules/auriga-dsh && node /opt/fixture/build.mjs manifest

FROM toolchain
COPY --from=build /opt /opt
COPY deploy/compose/real-dsh/start.mjs /opt/fixture/start.mjs
ENV HOME=/home/dsh DSH_TELEMETRY_DISABLED=1 DSH_TELEMETRY_MODE=DISABLED
WORKDIR /home/dsh
# Agent overrides this with the allocated uid >= 200000; never depend on root.
USER 200000:200000
EXPOSE 7860
ENTRYPOINT ["node", "/opt/fixture/start.mjs"]
