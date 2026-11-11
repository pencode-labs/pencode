# PEB v19 TypeScript SDK

Node.js 22+ / Bun 的扩展 SDK，与 `peb` Go 包使用相同的二进制帧、字段顺序、大小限制和 UTF-8 编码。标准输出只用于协议，日志使用 `console.error`。不使用 JSON-RPC。

`src/index.ts` 导出全部 v19 消息的 codec、类型、枚举、`Reader`、`Writer`、`Encoder`、`Decoder`、`serveExtension` 和 `Publisher`。通过 `encode(codec, value, encoder?)` / `decode(codec, buffer)` 使用底层消息；编码参数可以省略零值字段，解码结果始终具有完整字段。`view: null` 保留 DOM，`content: null` 保留文档；`content: ''` 清空文档。

```ts
import { ActionLocation, Match, serveExtension } from '@pencode/peb';

await serveExtension({
  actions: [{
    id: 'preview', text: '预览', icon: 'resources/preview.svg',
    location: ActionLocation.OpenWith,
    selectors: [{ match: Match.FileSuffix, pattern: '.txt' }],
  }],
}, {
  editor(message) {
    return { view: { html: '<p>只读预览</p>', css: '' } };
  },
});
```

开发时用本地依赖 `"@pencode/peb": "file:../../peb/ts"`（参见 `extensions/office-viewer`），构建时将 SDK 与业务依赖一起打包到 JavaScript。当前 SDK 未发布到 npm。单独检出插件时，放在 Pencode 的 `extensions/` 下，以便使用对应版本的本地 SDK。

清单的 `entry` 指向编译产物，例如 `dist/main.mjs`。服务端按 `.js` / `.mjs` / `.cjs` 后缀识别 JavaScript，使用工作区主机 PATH 中的 `node`，没有时使用 `bun`；两者都不存在时给出明确诊断。不会直接执行 `.ts`，不会调用 shell，也不会在加载时安装依赖。ESM 推荐使用 `.mjs`；`.js` 的模块类型由插件根目录的 `package.json` 决定。原生入口继续直接运行。远程项目在远端解析运行时并执行扩展。

`serveExtension` 串行调用 editor、settings、fileChanges、lifecycle、action 和 uiEvent 回调。`activate(publisher, signal)` 在 action 会话激活时调用一次；启动后台任务后应及时返回，并在 `signal` 取消时退出。`Publisher.update/remove/show/closeUI` 将主动推送与请求回复串行写入输出，`show` 支持完整视图和显式 `ViewPatch`；此 SDK 不自动生成 region patch。handler 抛出的普通错误回复 Failure，格式错误终止协议会话。Shutdown / EOF 释放输入迭代器并取消后台任务。

Settings 的默认值和快照值是 **JSON 字符串**，例如 `default: 'true'` 或 `default: '"auto"'`，与 Go 的 `json.RawMessage` 一致，保留 int64 的精度；消费时按需 `JSON.parse`，大整数可用 `BigInt(raw)`。

Reader 的 payload 借用缓冲区，仅在下一次 read 前有效。Encoder 保留可扩容缓冲区，结果在下一次 reset / encode 前有效。Writer 必须由单一调用者串行使用；`serveExtension` / `Publisher` 已处理写入顺序和背压。

```sh
npm ci
npm run check
npm test
```

跨语言测试从当前 Go SDK 生成所有消息的 fixture，再由 TS 解码、重编码并逐字节比较；另覆盖任意拆包、粘包、截断、错误版本和实际服务会话。

语言包通过 `languagePacks: [{ id, label, path }]` 注册，使用 PEB v19 的 `LanguagePacks` 能力；格式与 Go 相同。参见[语言包文档与示例](../../docs/localization.zh-CN.md)。
