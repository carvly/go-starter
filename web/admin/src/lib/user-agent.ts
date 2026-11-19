// describeUserAgent 把 User-Agent 换成"浏览器 主版本 · 系统"，认不出来时原样返回。
// 只用于展示，不做任何判断：User-Agent 由客户端自己填，可以伪造。
export function describeUserAgent(ua: string): string {
  const browser = detectBrowser(ua);
  if (!browser) return ua;
  const os = detectOS(ua);
  return os ? `${browser} · ${os}` : browser;
}

// 顺序有意义：Edge、Opera 的 UA 里也带 Chrome，Chrome 的 UA 里也带 Safari。
const browsers: [name: string, pattern: RegExp][] = [
  ["Edge", /Edg(?:e|A|iOS)?\/(\d+)/],
  ["Opera", /OPR\/(\d+)/],
  ["Firefox", /(?:Firefox|FxiOS)\/(\d+)/],
  ["Chrome", /(?:Chrome|CriOS)\/(\d+)/],
  ["Safari", /Version\/(\d+)[.\d]* (?:Mobile\/\S+ )?Safari\//],
];

function detectBrowser(ua: string): string | undefined {
  for (const [name, pattern] of browsers) {
    const m = pattern.exec(ua);
    if (m) return `${name} ${m[1]}`;
  }
  // 命令行工具：curl/8.21.0、python-requests/2.32.3
  const tool = /^([\w.-]+)\/([\w.-]+)$/.exec(ua.trim());
  return tool ? `${tool[1]} ${tool[2]}` : undefined;
}

const systems: [name: string, pattern: RegExp][] = [
  ["Windows", /Windows/],
  ["Android", /Android/],
  ["iOS", /iPhone|iPad|iPod/],
  ["macOS", /Mac OS X|Macintosh/],
  ["ChromeOS", /CrOS/],
  ["Linux", /Linux/],
];

function detectOS(ua: string): string | undefined {
  return systems.find(([, pattern]) => pattern.test(ua))?.[0];
}
