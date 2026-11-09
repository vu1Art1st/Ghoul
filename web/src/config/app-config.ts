import packageJson from "../../package.json";

const currentYear = new Date().getFullYear();

export const APP_CONFIG = {
  name: "Ghoul",
  version: packageJson.version,
  copyright: `© ${currentYear}, Ghoul.`,
  meta: {
    title: "Ghoul — 自主渗透测试控制台",
    description: "LLM 驱动的自主渗透测试系统控制台",
  },
};
