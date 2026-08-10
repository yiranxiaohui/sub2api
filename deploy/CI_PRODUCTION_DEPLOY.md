# CI production deployment

The Release workflow automatically deploys the exact GHCR release image after
the release job succeeds. The deployment only recreates the `sub2api` service;
PostgreSQL and Redis are left running. A failed application health check rolls
the application container back to its previous image.

## Production host preparation

The production host must have Docker with the Compose plugin installed. The
deployment directory must already contain `.env` and one of the repository's
Compose files. Its Compose file must use the `SUB2API_IMAGE` variable included
in the current repository version.

The SSH user must be able to run Docker without an interactive password prompt.
For the first deployment after enabling this workflow, update the production
Compose file to the current repository version so its application image is:

```yaml
image: ${SUB2API_IMAGE:-ghcr.io/yiranxiaohui/sub2api:latest}
```

## GitHub production environment

Create an Environment named `production`, then configure these secrets:

| Secret | Description |
| --- | --- |
| `PRODUCTION_SSH_HOST` | Production SSH hostname or IP address |
| `PRODUCTION_SSH_PORT` | SSH port; defaults to `22` when omitted |
| `PRODUCTION_SSH_USER` | SSH login user |
| `PRODUCTION_SSH_PRIVATE_KEY` | Private key dedicated to CI deployment |
| `PRODUCTION_SSH_KNOWN_HOSTS` | Verified `known_hosts` entry for the production host |
| `PRODUCTION_DEPLOY_PATH` | Absolute directory containing `.env` and the Compose file |
| `PRODUCTION_GHCR_TOKEN` | Optional classic PAT with `read:packages` for a private GHCR image |

Set the environment variable `PRODUCTION_COMPOSE_FILE` when the production file
is not named `docker-compose.yml`, for example `docker-compose.local.yml`.

Verify the server host key out of band before storing it. One way to obtain the
candidate entry is:

```bash
ssh-keyscan -p 22 production.example.com
```

For extra control, add required reviewers or deployment branch/tag rules to the
`production` Environment. To disable automatic deployment without editing the
workflow, set the repository variable `PRODUCTION_AUTO_DEPLOY=false`.
