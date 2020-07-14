From golang:1.13-alpine
ENV GCSPROXY_VERSION=0.3.0

WORKDIR /app
COPY . .
RUN apk add --no-cache make \
  && make bin/gcsproxy \
  && cp bin/gcsproxy /usr/local/bin/gcsproxy \
  && chmod +x /usr/local/bin/gcsproxy
