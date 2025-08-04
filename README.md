# Get started

## Generate dev certificates & Build

openssl req -x509 -newkey rsa:4096 -keyout server.key -out server.crt -days 365 -nodes -subj "/CN=localhost"

go build -o bin/thrylos ./cmd/thrylos && ./bin/thrylos