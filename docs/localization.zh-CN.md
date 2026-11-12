# 界面语言与语言包

在「设置 → 外观 → 界面语言」中选择语言，立即生效并自动保存到 `settings.json` 的 `language` 字段。本地和远程工作区沿用各自的配置存储。默认保持简体中文（`zh-CN`）；内置英语（`en`）、日语（`ja`）、韩语（`ko`）、德语（`de`）、法语（`fr`）和西班牙语（`es`）。语言名称始终使用该语言的原生名称。

语言包使用 UTF-8 JSON，内置文件位于 `localization/locales/`。消息键使用界面的简体中文原文；`en.json` 可作为翻译模板。非中文语言缺少条目时先回退到英文，英文也缺少时显示原文。当前日、韩、德、法、西语言包覆盖主要界面，其余条目按上述规则回退。插件自身 HTML、编辑内容、文件名和用户输入不参与工作台文案替换。

## 通过扩展添加语言

语言包复用扩展的安装、启用、禁用和资源传输流程，包括 SSH / WebSocket 工作区。创建普通扩展清单 `resources/extension.json`，将 `entry` 指向注册程序。注册程序只需贡献静态资源，不需要常驻进程。

例如，在 `resources/locales/it.json` 中提供：

```json
{
  "id": "it",
  "label": "Italiano",
  "messages": {
    "设置": "Impostazioni",
    "外观": "Aspetto",
    "界面语言": "Lingua dell’interfaccia",
    "保存": "Salva",
    "行 %d · %d 字符": "Riga %d · %d caratteri"
  }
}
```

Go 注册程序：

```go
package main

import (
    "log"
    "os"

    "github.com/pencode-labs/pencode/peb"
)

func main() {
    if err := peb.Serve(os.Stdin, os.Stdout, peb.Contributions{
        LanguagePacks: []peb.LanguagePack{{
            ID: "it", Label: "Italiano", Path: "resources/locales/it.json",
        }},
    }); err != nil {
        log.Fatal(err)
    }
}
```

TypeScript 注册程序使用同一个文件：

```ts
import { serveExtension } from '@pencode/peb';

await serveExtension({
  languagePacks: [{
    id: 'it', label: 'Italiano', path: 'resources/locales/it.json',
  }],
});
```

扩展加载成功后，Italiano 自动出现在语言选择器中。ID 使用语言标签形式，如 `it`、`pt-BR`，不能包含路径；声明的 ID、名称必须与 JSON 一致，路径必须位于扩展的 `resources/` 下。每个扩展最多提供 64 个语言包，每个 JSON 不超过 1 MiB，最多 8192 个消息。翻译中的 `%d`、`%s`、`%v` 等格式参数应与原文保持一致。

内置语言 ID 保留，重复 ID 不覆盖已经注册的语言。禁用或卸载当前语言包时回退到英文，但保留设置中的 ID；重新启用或安装后自动恢复。应用启动或连接工作区时也会保留尚未加载的插件语言选择。

语言查询只读取内存中的不可变映射，不进行文件读取、格式化或内存分配。模板的静态文案绑定在创建工作台视图时编译，编辑器区域的局部更新跳过这部分工作。

PEB 当前为 v20；语言包在 v19 引入，提供 `LanguagePacks = 512` 能力以及 Register 尾部的 `languagePacks` 数组。Go 和 TypeScript 扩展均需使用当前 SDK 重新构建；可以运行 `make install-extensions` 构建并安装项目内的扩展。
