FROM node:22-alpine
WORKDIR /app
COPY server.js /app/server.js
USER node
EXPOSE 7860
ENV PORT=7860
CMD ["node", "/app/server.js"]
