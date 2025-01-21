FROM alpine:latest
RUN apk --no-cache --no-progress add ca-certificates
WORKDIR /naiveswitcher
COPY naiveswitcher ./

ENTRYPOINT ["/naiveswitcher/naiveswitcher"]
