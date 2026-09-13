import type { Config } from "tailwindcss";

const config: Config = {
  content: ["./app/**/*.{ts,tsx}", "./components/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        surface: "#0b1020",
        panel: "#141a2e",
        accent: "#4f8cff",
        danger: "#ff5c5c",
        warn: "#ffb84d",
        ok: "#3ddc97",
      },
    },
  },
  plugins: [],
};

export default config;
