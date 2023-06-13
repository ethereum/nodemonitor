# Build in a stock Go builder container
FROM golang:1.20-alpine as builder

RUN apk add --no-cache gcc musl-dev linux-headers

RUN mkdir -p /nodemonitor/nodes
ADD *.go /nodemonitor
ADD go.mod /nodemonitor
ADD go.sum /nodemonitor
ADD nodes /nodemonitor/nodes
RUN cd /nodemonitor && go build -ldflags "-X google.golang.org/protobuf/reflect/protoregistry.conflictPolicy=warn" .

# Pull binary into a second stage deploy alpine container
FROM alpine:latest
COPY --from=builder /nodemonitor/nodemonitor /usr/local/bin/

ADD www/index.html /www/index.html
ADD www/*.js /www/
RUN mkdir -p /www/hashes
RUN mkdir -p /www/vulns
RUN mkdir -p /www/badblocks

EXPOSE 8080
ENTRYPOINT ["nodemonitor"]
