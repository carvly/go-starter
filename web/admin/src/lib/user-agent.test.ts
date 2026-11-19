import { describe, expect, it } from "vitest";

import { describeUserAgent } from "./user-agent";

describe("describeUserAgent", () => {
  it.each([
    ["Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36", "Chrome 157 · Linux"],
    [
      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36 Edg/157.0.0.0",
      "Edge 157 · Windows",
    ],
    ["Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:150.0) Gecko/20100101 Firefox/150.0", "Firefox 150 · macOS"],
    [
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.1 Safari/605.1.15",
      "Safari 26 · macOS",
    ],
    [
      "Mozilla/5.0 (iPhone; CPU iPhone OS 26_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.1 Mobile/15E148 Safari/604.1",
      "Safari 26 · iOS",
    ],
    [
      "Mozilla/5.0 (Linux; Android 16) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Mobile Safari/537.36",
      "Chrome 157 · Android",
    ],
    ["curl/8.21.0", "curl 8.21.0"],
    ["something unknown", "something unknown"],
  ])("%s", (ua, want) => {
    expect(describeUserAgent(ua)).toBe(want);
  });
});
