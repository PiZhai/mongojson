FROM node:24-bookworm-slim AS build
WORKDIR /app

ARG VITE_INCLUDED_MODULES=""
ENV VITE_INCLUDED_MODULES=$VITE_INCLUDED_MODULES

COPY package.json package-lock.json* ./
RUN npm config set registry https://registry.npmmirror.com \
    && npm install

COPY . .
RUN npm run build

FROM nginx:1.29-alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
