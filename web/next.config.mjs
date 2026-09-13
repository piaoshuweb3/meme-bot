/** @type {import('next').NextConfig} */
import path from "node:path";

const nextConfig = {
  reactStrictMode: true,

  // 后端 API 地址通过环境变量注入（NEXT_PUBLIC_API_BASE），默认本地 8080
  env: {
    NEXT_PUBLIC_API_BASE: process.env.NEXT_PUBLIC_API_BASE ?? "http://localhost:8080",
  },

  // 明确构建根目录：避免宿主机上存在多个 lockfile 时被误判（构建警告）
  outputFileTracingRoot: path.resolve(process.cwd()),
};

export default nextConfig;
