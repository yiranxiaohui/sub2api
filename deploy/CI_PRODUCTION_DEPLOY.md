# CI production deployment

The Release workflow automatically deploys the exact GHCR release image after
the release job succeeds. The deployment only recreates the `sub2api` service;
PostgreSQL and Redis are left running. A failed application health check rolls
the application container back to its previous image.

## Production host preparation

The production host must have Docker with the Compose plugin installed. The
deployment directory must already contain the Compose file used by the running
`sub2api` container.

The SSH user must be able to run Docker without an interactive password prompt.
The workflow reads the image name from the existing `sub2api` container, pulls
the exact new GHCR release, retags it to that existing image name, and recreates
only the application service through the existing Compose project. The server's
Compose file does not need to be modified. Digest-pinned images are rejected
because they cannot be safely retagged.

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
