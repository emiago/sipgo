# OpenSIPS Docker Image
http://www.opensips.org/

Docker recipe for building and starting an OpenSIPS image

## Building the image
You can build the docker image by running:
```
make build
```

This command will build a docker image with OpenSIPS master version taken from
the git repository. To build a different git version, you can run:
```
OPENSIPS_VERSION=2.2 make build
```

To build with MySQL support:
```
OPENSIPS_EXTRA_MODULES=opensips-mysql-module make build
```

To start the image, simply run:
```
make start
```

## Variables
You can set different variables to tune your deployment:
 * `OPENSIPS_VERSION` - sets the opensips version (Default: `3.1`)
 * `OPENSIPS_BUILD` - specifies the build to use, `nightly` or `releases` (Default: `releases`)
 * `OPENSIPS_DOCKER_TAG` - indicates the docker tag (Default: `latest`)
 * `OPENSIPS_CLI` - specifies whether to install opensips-cli (`true`) or not (`false`) (Default: `true`)
 * `OPENSIPS_EXTRA_MODULES` - specifies extra opensips modules to install (Default: no other module)

## Packages on DockerHub

Released docker packages are visible on DockerHub
https://hub.docker.com/r/opensips/opensips
