#checkov:skip=CKV_DOCKER_2:CloudNativePG owns PostgreSQL startup and health probes and overrides the container entrypoint.
FROM ghcr.io/cloudnative-pg/postgresql:17.11-minimal-trixie@sha256:d78e771decf39071aa8bfb96684e8b7e6e5f3c6e00a945404249756db2c6c712 AS build
USER 0

# Match server headers to the immutable runtime's PostgreSQL package exactly.
RUN apt-get update \
    && apt-get install --no-install-recommends -y \
        build-essential=12.12 \
        flex=2.6.4-8.2+b4 \
        bison=2:3.8.2+dfsg-1+b2 \
        postgresql-server-dev-17=17.11-1.pgdg13+2 \
        libreadline-dev=8.2-6 \
        zlib1g-dev=1:1.3.dfsg+really1.3.1-1+b1 \
    && rm -rf /var/lib/apt/lists/*

COPY age-release-key.asc age-release.asc /verification/
WORKDIR /src
RUN /usr/lib/apt/apt-helper -o Acquire::https::Timeout=90 -o Acquire::Retries=3 download-file \
        https://archive.apache.org/dist/age/PG17/1.7.0/apache-age-1.7.0-src.tar.gz /src/age.tar.gz \
        SHA512:301f02088f6828adb8cfa70e7701d9fb541ee9212de97a99f2cf9106e0b0d8bce82f95c20579302bdac5efc3d5ffe35732755b1d093526cec4beba9f30ec82f0 \
    && mkdir -m 700 /verification/gnupg \
    && gpg --batch --homedir /verification/gnupg --import /verification/age-release-key.asc \
    && gpg --batch --homedir /verification/gnupg --verify /verification/age-release.asc /src/age.tar.gz \
    && tar -xzf /src/age.tar.gz --strip-components=1 \
    && rm /src/age.tar.gz \
    && make -j2 PG_CONFIG=/usr/bin/pg_config with_llvm=no \
    && make PG_CONFIG=/usr/bin/pg_config with_llvm=no DESTDIR=/out install \
    && mkdir -p /out/usr/lib/postgresql/17/lib/plugins /out/usr/share/doc/postgresql-age \
    && ln -s ../age.so /out/usr/lib/postgresql/17/lib/plugins/age.so \
    && cp LICENSE NOTICE /out/usr/share/doc/postgresql-age/

FROM ghcr.io/cloudnative-pg/postgresql:17.11-minimal-trixie@sha256:d78e771decf39071aa8bfb96684e8b7e6e5f3c6e00a945404249756db2c6c712
USER 0
# Apply available security updates without importing the build toolchain.
RUN apt-get update \
    && apt-get install --no-install-recommends -y \
        libpcre2-8-0=10.46-1~deb13u3 \
        libssl3t64=3.5.7-1~deb13u3 \
        openssl=3.5.7-1~deb13u3 \
        openssl-provider-legacy=3.5.7-1~deb13u3 \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/usr/ /usr/
LABEL org.opencontainers.image.title="Data Product Controller PostgreSQL with Apache AGE" \
      org.opencontainers.image.source="https://github.com/devantler-tech/data-product-controller" \
      org.opencontainers.image.licenses="PostgreSQL AND Apache-2.0" \
      io.devantler.postgresql.major="17" \
      io.devantler.postgresql.version="17.11" \
      io.devantler.age.version="1.7.0"
USER 26
