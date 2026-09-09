FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
COPY beszel /beszel
RUN chmod +x /beszel
EXPOSE 8090
ENTRYPOINT ["/beszel"]
CMD ["serve", "--http=0.0.0.0:8090"]
