import type { NextConfig } from "next";
const nextConfig: NextConfig = { output: "standalone", images: { remotePatterns: [{ protocol: "https", hostname: "cdn.acme.example" }] } };
export default nextConfig;
