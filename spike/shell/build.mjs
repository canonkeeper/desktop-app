// SPDX-License-Identifier: AGPL-3.0-only
// Bundles the probe page: the map (sigma + graphology) and the layout worker (ForceAtlas2), as
// the app's map does, into dist/ with no network resources.
import { build } from "esbuild";
import { copyFileSync, mkdirSync } from "node:fs";

mkdirSync("dist", { recursive: true });
copyFileSync("src/index.html", "dist/index.html");
copyFileSync("src/style.css", "dist/style.css");
await build({
  entryPoints: { map: "src/map.js", layout: "src/layout.js" },
  bundle: true,
  format: "esm",
  target: ["safari15", "chrome100"],
  minify: true,
  outdir: "dist",
});
