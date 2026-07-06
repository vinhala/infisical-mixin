# infisical-mixin

A Docker mixin layer for adding [Infisical](https://infisical.com/) secret
loading to application images.

The published image contains:

- `/usr/local/bin/infisical`: the official Infisical CLI
- `/usr/local/bin/infisical-mixin`: a small entrypoint that fetches secrets,
  applies aliases from `infisical_mapping.yml`, and then `exec`s your app

## Usage

Copy the mixin binaries into your application image and use `infisical-mixin`
as the entrypoint.

```dockerfile
FROM ghcr.io/vinhala/infisical-mixin:v0.1.0 AS infisical-mixin

FROM node:22-alpine
WORKDIR /app

COPY --from=infisical-mixin /usr/local/bin/infisical /usr/local/bin/infisical
COPY --from=infisical-mixin /usr/local/bin/infisical-mixin /usr/local/bin/infisical-mixin

COPY package*.json ./
RUN npm ci --omit=dev
COPY . .

ENTRYPOINT ["/usr/local/bin/infisical-mixin"]
CMD ["node", "server.js"]
```

If your base image does not include CA certificates, install them in the app
image or copy the bundle from the mixin image:

```dockerfile
COPY --from=infisical-mixin /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
```

## Runtime Configuration

Set these environment variables when running the final container:

| Variable | Description |
| --- | --- |
| `INFISICAL_TOKEN` | Existing machine identity or service token. |
| `INFISICAL_MACHINE_CLIENT_ID` | Universal Auth client ID. Used only when `INFISICAL_TOKEN` is unset. |
| `INFISICAL_MACHINE_CLIENT_SECRET` | Universal Auth client secret. Used only when `INFISICAL_TOKEN` is unset. |
| `INFISICAL_PROJECT_ID` | Infisical project ID. Required. |
| `PROJECT_ID` | Fallback for `INFISICAL_PROJECT_ID`. |
| `INFISICAL_ENV` | Infisical environment slug, such as `dev`, `staging`, or `prod`. |
| `INFISICAL_SECRET_ENV` | Fallback for `INFISICAL_ENV`. |
| `INFISICAL_PATHS` | Comma-separated secret paths. Each path is passed as `--path`. |
| `INFISICAL_API_URL` | Custom Infisical API URL, passed as `--domain`. |
| `INFISICAL_TAGS` | Tag filter, passed as `--tags`. |
| `INFISICAL_EXPAND` | Optional `true` or `false`, passed as `--expand`. |
| `INFISICAL_INCLUDE_IMPORTS` | Optional `true` or `false`, passed as `--include-imports`. |

Examples:

```sh
docker run --rm \
  -e INFISICAL_TOKEN="$INFISICAL_TOKEN" \
  -e INFISICAL_PROJECT_ID="00000000-0000-0000-0000-000000000000" \
  -e INFISICAL_ENV="prod" \
  ghcr.io/your-org/your-app:latest
```

```sh
docker run --rm \
  -e INFISICAL_MACHINE_CLIENT_ID="$INFISICAL_MACHINE_CLIENT_ID" \
  -e INFISICAL_MACHINE_CLIENT_SECRET="$INFISICAL_MACHINE_CLIENT_SECRET" \
  -e INFISICAL_PROJECT_ID="00000000-0000-0000-0000-000000000000" \
  ghcr.io/your-org/your-app:latest
```

## Secret Aliases

If `infisical_mapping.yml` exists in the container working directory,
`infisical-mixin` maps fetched secrets to aliases before starting the app.

```yaml
SERVICE_1_DATABASE_URL:
  aliases:
    - POSTGRES_URL

SERVICE_2_DATABASE_URL:
  aliases:
    - POSTGRES_URL
```

Alias behavior:

- The source secret must exist in the Infisical export.
- Alias names must be non-empty and cannot contain `=`.
- If more than one source maps to the same alias, the later YAML entry wins.
- Aliases are applied after fetched secrets, so an alias can override an
  existing variable or another fetched secret key.

## Existing Entrypoints

If your image already has an entrypoint, move that command to `CMD` or wrap it
behind `infisical-mixin`.

```dockerfile
ENTRYPOINT ["/usr/local/bin/infisical-mixin"]
CMD ["/usr/local/bin/original-entrypoint", "start"]
```

`infisical-mixin` uses `exec`, so the application command receives container
signals directly.

## Publishing

The GitHub Actions workflow publishes multi-architecture images to:

```text
ghcr.io/vinhala/infisical-mixin
```

Publishing runs on tag pushes matching `v*`, for example:

```sh
git tag v0.1.0
git push origin v0.1.0
```

The workflow publishes the exact tag, semver aliases, and `latest`.

## Development

Run the Go tests in Docker:

```sh
docker build --target test .
```

Run the build-time smoke test with a fake Infisical CLI:

```sh
docker build --target smoke .
```

Build the final image locally:

```sh
docker build -t infisical-mixin .
```
