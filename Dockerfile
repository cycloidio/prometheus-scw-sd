ARG ARCH="amd64"
ARG OS="linux"
FROM quay.io/prometheus/busybox-${OS}-${ARCH}:latest
LABEL maintainer="Scaleway <opensource@scaleway.com>"

ARG ARCH="amd64"
ARG OS="linux"
COPY .build/${OS}-${ARCH}/prometheus-scw-sd /bin/prometheus-scw-sd
COPY LICENSE                                /LICENSE

RUN mkdir -p /prometheus-scw-sd && \
    chown -R nobody:nogroup /prometheus-scw-sd

USER       nobody
VOLUME     [ "/prometheus-scw-sd" ]
WORKDIR    /prometheus-scw-sd
ENTRYPOINT [ "/bin/prometheus-scw-sd" ]
CMD        [ "--help" ]
