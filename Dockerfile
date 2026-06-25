# syntax=docker/dockerfile:1

FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/atem .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates \
	&& adduser -D -H -u 10001 atem

COPY --from=build /out/atem /usr/local/bin/atem

USER atem
ENV XDG_CONFIG_HOME=/tmp/.config
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/atem"]
CMD ["serve", "--addr", ":8080", "--toolset", "m365"]
