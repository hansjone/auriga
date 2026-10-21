# NODE_IMAGE must be the reviewed node:22.19.0-bookworm-slim manifest digest.
# Build with scripts/build-real-dsh.mjs; tags alone are rejected there.
ARG NODE_IMAGE
FROM ${NODE_IMAGE} AS build
WORKDIR /opt/dsh
COPY deploy/compose/real-dsh/package*.json ./
COPY deploy/compose/real-dsh/versions.json /opt/fixture/versions.json
COPY deploy/compose/real-dsh/build.mjs deploy/compose/real-dsh/inputs.mjs /opt/fixture/
RUN node /opt/fixture/build.mjs check
RUN npm ci --omit=dev --include=optional --ignore-scripts --bin-links=false --no-audit --no-fund --registry=https://registry.npmjs.org --os=linux --cpu=x64 --libc=glibc
WORKDIR /opt/auriga-dsh
COPY clients/dsh-plugin/package*.json ./
RUN node /opt/fixture/build.mjs plugin-lock
RUN npm ci --omit=dev --include=optional --ignore-scripts --bin-links=false --no-audit --no-fund --registry=https://registry.npmjs.org
COPY clients/dsh-plugin/src ./src
COPY clients/dsh-plugin/LICENSE ./LICENSE
RUN ln -s /opt/auriga-dsh /opt/dsh/node_modules/auriga-dsh && node /opt/fixture/build.mjs manifest

FROM ${NODE_IMAGE}
COPY --from=build /opt /opt
COPY deploy/compose/real-dsh/start.mjs /opt/fixture/start.mjs
ENV HOME=/home/dsh DSH_VERSION=0.2.0-rc.2 DSH_TELEMETRY_DISABLED=1 DSH_TELEMETRY_MODE=DISABLED
WORKDIR /home/dsh
# Agent overrides this with the allocated uid >= 200000; never depend on root.
USER 200000:200000
EXPOSE 7860
ENTRYPOINT ["node", "/opt/fixture/start.mjs"]
