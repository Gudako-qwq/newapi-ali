import path from 'node:path'

import { defineConfig } from '@rsbuild/core'
import { pluginReact } from '@rsbuild/plugin-react'
import { pluginTailwindcss } from '@rsbuild/plugin-tailwindcss'

export default defineConfig({
  plugins: [pluginReact(), pluginTailwindcss({ optimize: false })],
  source: { entry: { index: './src/main.jsx' } },
  resolve: { alias: { '@': path.resolve('src') } },
  html: { template: './index.html' },
  output: { sourceMap: false, distPath: { root: 'dist' } },
})
