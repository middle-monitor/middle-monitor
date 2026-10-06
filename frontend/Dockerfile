# The bundle is platform independent: build it natively, ship it on any arch.
FROM --platform=$BUILDPLATFORM node:22-alpine AS builder

WORKDIR /app
COPY package*.json ./
RUN npm ci

COPY . .
# The API is reached on the same origin under /api, so no API URL is baked in.
#
# Self-monitoring opt-in: the service token of the middle-front application,
# read by @middle-monitor/web. It ships in public JS by construction, so it must
# be a services.token (ingest-only) and never an mm_ org API key.
ARG VITE_MIDDLE_MONITOR_TOKEN=
ENV VITE_MIDDLE_MONITOR_TOKEN=$VITE_MIDDLE_MONITOR_TOKEN
# Optional Plausible analytics; empty means no tracker in the build.
ARG VITE_PLAUSIBLE_SRC=
ARG VITE_PLAUSIBLE_DOMAIN=
ENV VITE_PLAUSIBLE_SRC=$VITE_PLAUSIBLE_SRC VITE_PLAUSIBLE_DOMAIN=$VITE_PLAUSIBLE_DOMAIN
RUN npm run build

FROM nginx:alpine
# Rendered by the image entrypoint (envsubst) so the CSP can allow ANALYTICS_ORIGIN.
COPY nginx.conf /etc/nginx/templates/default.conf.template
ENV ANALYTICS_ORIGIN=
COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
