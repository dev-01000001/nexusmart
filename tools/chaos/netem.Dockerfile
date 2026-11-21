# Minimal local Linux tc netem helper for `slow-payment` fault injection.
FROM alpine:3.20
RUN apk add --no-cache iproute2-tc
ENTRYPOINT ["tc"]
