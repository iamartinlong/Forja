# 多阶段构建：零依赖模板无需安装层；引入依赖后在此补 `npm ci`
FROM node:20-alpine

WORKDIR /app
COPY . .

ENV NODE_ENV=production \
    PORT=8080
EXPOSE 8080

# 非 root 运行（node 镜像内置 node 用户）
USER node

CMD ["node", "src/server.js"]
