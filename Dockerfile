FROM docker.io/library/golang:1.23 as builder
WORKDIR /app/
COPY go.mod go.sum /app/
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -v

FROM alpine
COPY --from=builder /app/imagen-telegram-bot /app/imagen-telegram-bot

ENTRYPOINT ["/app/imagen-telegram-bot"]
ENV OPENAI_API_KEY= BOT_TOKEN= ALLOWED_USERIDS= ADMIN_USERIDS= ALLOWED_GROUPIDS=
