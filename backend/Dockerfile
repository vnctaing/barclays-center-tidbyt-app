FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /barclays-backend .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /barclays-backend /barclays-backend
CMD ["/barclays-backend"]
