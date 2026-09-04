# syntax=docker/dockerfile:1

# --- Build stage -------------------------------------------------------------
FROM node:22-alpine AS build
WORKDIR /src

COPY package.json package-lock.json* ./
RUN npm ci || npm install

COPY . .
RUN npm run build

# --- Runtime stage -----------------------------------------------------------
FROM nginx:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
EXPOSE 80