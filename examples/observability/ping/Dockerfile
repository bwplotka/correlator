FROM golang:1.18 AS build-env
WORKDIR /tmp/workdir

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build
RUN cd pinger && CGO_ENABLED=0 GOOS=linux go build

FROM scratch

COPY --from=build-env /tmp/workdir/ping /bin/ping
COPY --from=build-env /tmp/workdir/pinger/pinger /bin/pinger

CMD ["/bin/ping", "-set-version=v0.0.7", "-latency=90%500ms,10%200ms", "-success-prob=65"]