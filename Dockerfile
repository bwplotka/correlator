FROM golang:1.18 AS build-env
WORKDIR /tmp/workdir

COPY go.mod go.mod
COPY go.sum go.sum
COPY cmd cmd
COPY pkg pkg
RUN cd cmd/correlator && CGO_ENABLED=0 GOOS=linux go build

FROM scratch

COPY --from=build-env /tmp/workdir/cmd/correlator /bin/correlator

CMD ["/bin/correlator"]