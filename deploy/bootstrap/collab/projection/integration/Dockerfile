ARG JAVA_IMAGE=auriga-msgnet-n-cn-jre:local
FROM ${JAVA_IMAGE} AS java
FROM golang:1.27.3
COPY --from=java /opt/java/openjdk /opt/java/openjdk
ENV PATH="/opt/java/openjdk/bin:${PATH}"
