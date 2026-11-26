# Speklo web UI → ghcr.io/<owner>/<repo>/web:<version>
# Build context: server/web
#
# nginx serves the React app and forwards /api, /auth and /mock to the API
# container, so the browser sees one origin (the session cookie and the
# Keycloak redirect need that).

# The build output is plain files, so build once on the runner's architecture.
FROM --platform=$BUILDPLATFORM node:26-alpine AS build
WORKDIR /web
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . .
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.27-alpine
# Where the API container answers. Override for Kubernetes or a different name.
ENV SPEKLO_API_UPSTREAM=http://api:8080
COPY nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /web/dist /usr/share/nginx/html
EXPOSE 8080
