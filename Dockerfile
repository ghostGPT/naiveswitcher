FROM alpine:latest
ARG TARGETPLATFORM
RUN apk --no-cache --no-progress add ca-certificates
WORKDIR /naiveswitcher
COPY ${TARGETPLATFORM}/naiveswitcher ./

ENTRYPOINT ["/naiveswitcher/naiveswitcher"]
