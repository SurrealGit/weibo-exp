<h1 align="center">
  <img src="./assets/readme/logo.svg" alt="weibo-exp：终端与任务完成标记" width="160" height="160">
  <br>
  weibo-exp
</h1>

<p align="center">
  <img src="https://img.shields.io/badge/built%20with-Go-f56d32?style=flat-square&amp;logo=go&amp;logoColor=white&amp;labelColor=555555" alt="使用 Go 开发">
  <img src="https://img.shields.io/badge/runtime%20deps-0-44cc11?style=flat-square&amp;logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyNCIgaGVpZ2h0PSIyNCIgdmlld0JveD0iMCAwIDI0IDI0IiBmaWxsPSJub25lIiBzdHJva2U9IndoaXRlIiBzdHJva2Utd2lkdGg9IjIiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgc3Ryb2tlLWxpbmVqb2luPSJyb3VuZCI%2BPHBhdGggZD0iTTExIDIxLjczYTIgMiAwIDAgMCAyIDBsNy00QTIgMiAwIDAgMCAyMSAxNlY4YTIgMiAwIDAgMC0xLTEuNzNsLTctNGEyIDIgMCAwIDAtMiAwbC03IDRBMiAyIDAgMCAwIDMgOHY4YTIgMiAwIDAgMCAxIDEuNzN6Ii8%2BPHBhdGggZD0iTTEyIDIyVjEyIi8%2BPHBvbHlsaW5lIHBvaW50cz0iMy4yOSA3IDEyIDEyIDIwLjcxIDciLz48cGF0aCBkPSJtNy41IDQuMjcgOSA1LjE1Ii8%2BPC9zdmc%2B&amp;logoColor=white&amp;labelColor=555555" alt="额外运行时依赖：0">
  <img src="https://img.shields.io/badge/platforms-macOS%20%7C%20Windows%20%7C%20Linux-7d81f7?style=flat-square&amp;logo=data%3Aimage%2Fsvg%2Bxml%3Bbase64%2CPHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyNCIgaGVpZ2h0PSIyNCIgdmlld0JveD0iMCAwIDI0IDI0IiBmaWxsPSJub25lIiBzdHJva2U9IndoaXRlIiBzdHJva2Utd2lkdGg9IjIiIHN0cm9rZS1saW5lY2FwPSJyb3VuZCIgc3Ryb2tlLWxpbmVqb2luPSJyb3VuZCI%2BPHJlY3Qgd2lkdGg9IjIwIiBoZWlnaHQ9IjE0IiB4PSIyIiB5PSIzIiByeD0iMiIvPjxsaW5lIHgxPSI4IiB4Mj0iMTYiIHkxPSIyMSIgeTI9IjIxIi8%2BPGxpbmUgeDE9IjEyIiB4Mj0iMTIiIHkxPSIxNyIgeTI9IjIxIi8%2BPC9zdmc%2B&amp;logoColor=white&amp;labelColor=555555" alt="支持 macOS、Windows 和 Linux">
</p>
<p align="center">
  <strong>微博超话经验助手</strong>
  <br>
  每天的超话任务，交给电脑自动完成。
</p>

<p align="center">
  自动签到、评论与转发后删除，支持每日定时执行和自定义评论池。
  <br>
  单文件命令行工具，零额外运行时依赖，支持 macOS、Windows 和 Linux。
</p>

<p align="center">
  <a href="https://github.com/SurrealGit/weibo-exp/releases/latest">下载最新版</a>
  ·
  <a href="#快速开始">快速开始</a>
  ·
  <a href="#命令列表">命令列表</a>
  ·
  <a href="https://github.com/SurrealGit/weibo-exp/issues">问题反馈</a>
</p>

## 功能亮点

- **开箱即用**：登录和任务均由程序独立完成，无需配置运行环境或启动浏览器。
- **每日自动执行**：默认每天 10:00 开始，每 4 小时检查补跑，至当天结束；已完成的任务自动跳过。
- **范围自由选择**：自动获取关注列表，支持全部处理，也可按序号或名称选择。
- **按需配置互动**：默认每个超话每日评论 4 条、转发 2 条，支持自定义数量与评论内容。
- **自动清理互动**：评论和转发发布后请求删除；中途失败时保存进度，下次优先恢复清理。
- **进度一目了然**：查看今日完成情况、各超话详情和带时间戳的运行日志。

## 经验值与超 LIKE

默认每天为每个超话完成 **签到、4 次评论、2 次转发**。按照[微博官方超话帮助](https://huati.weibo.cn/about/super)中的普通计分规则，不使用经验加速卡、评论均按普通评论计分时：

| 连续签到天数 | 签到 | 评论 4 次 | 转发 2 次 | 每日合计 |
| --- | --- | --- | --- | --- |
| 第 1 天 | 4 | 4 | 2 | **10 分** |
| 第 2–29 天 | 6 | 4 | 2 | **12 分** |
| 第 30 天起 | 8 | 4 | 2 | **14 分** |

零互动帖首评每次可得 2 分；程序按可用帖子选择目标，首评加分未计入上表。使用经验加速卡时，请按手机 App 展示的规则和额度调整配置。

**超 LIKE 是近 7 天获取经验值达到 80 分的活跃头衔。** 若从首签开始每天完成上述任务并有效计分，**7 天可累计 `10 + 12 × 6 = 82` 分，达到这一门槛**；之后持续活跃，维持近 7 天经验值。头衔及当前规则查看方式：超话 → 我的 → 我的头衔 → 超like。

上表是规则测算，程序会在发布评论和转发后请求删除，实际经验到账、后续保留情况及头衔以微博 App 为准。经验值获取情况查看方式：超话 → 我的 → 赚经验。

## 快速开始

准备好已登录的微博手机 App，并在微博中关注想完成任务的超话。电脑保持联网。

### 1. 下载并解压

前往 [Releases（发行版本）](https://github.com/SurrealGit/weibo-exp/releases)，选择最新版并展开 Assets（下载文件），按下表选择压缩包，解压到一个方便找到的文件夹。

| 你的电脑 | 选择的文件名后缀 | 解压后的程序 |
| --- | --- | --- |
| Mac，Apple 芯片（M1、M2、M3、M4 等） | `darwin-arm64.tar.gz` | `weibo-exp` |
| Mac，Intel 处理器 | `darwin-amd64.tar.gz` | `weibo-exp` |
| Windows，Intel / AMD 64 位处理器 | `windows-amd64.zip` | `weibo-exp.exe` |
| Linux，Intel / AMD 64 位处理器 | `linux-amd64.tar.gz` | `weibo-exp` |

例如，Apple 芯片 Mac 对应 `weibo-exp-v0.7.0-darwin-arm64.tar.gz`。每个压缩包包含程序、使用说明及配套图片和许可声明。

Mac 可在苹果菜单的“关于本机”中查看芯片；Windows 可在“设置 → 系统 → 系统信息”中查看系统类型。Linux 自动执行使用用户级 systemd（243 或更新版本）；手动运行无需 systemd。

### 2. 安装程序

在解压后的文件夹打开终端，执行对应命令。

<details>
<summary>第一次使用终端？点这里</summary>

- **macOS**：按 `⌘ Space`，搜索并打开“终端”。输入 `cd` 和一个空格，把解压后的文件夹拖入窗口，然后按回车。
- **Windows**：在文件资源管理器中打开解压后的文件夹，在地址栏输入 `powershell`，然后按回车。
- **Linux**：在文件管理器中打开解压后的文件夹，选择“在终端中打开”。

复制下面对应系统的一行命令，粘贴到终端，再按回车即可。

</details>

| macOS / Linux | Windows PowerShell |
| --- | --- |
| `./weibo-exp install` | `.\weibo-exp.exe install` |

按提示输入 `yes`，程序会安装到固定目录，并配置当前用户的 PATH（系统查找命令的目录列表）。登录与每日自动执行分别在后续步骤中设置。

**安装完成后，请关闭并重新打开终端。** 接下来的命令在三个平台相同，可以在任意目录执行，无需切换到安装文件夹。

首次安装时的 `./` 或 `.\` 表示运行解压文件夹中的程序；安装后直接使用 `weibo-exp`。

### 3. 扫码登录

```sh
weibo-exp login
```

使用微博 App 扫描弹出的二维码，并在手机上确认登录。登录会话保存在本机。

默认处理全部关注超话，每个超话每日评论 4 条、转发 2 条，并为未签到的超话签到。运行前可参考[日常使用](#日常使用)和[评论池](#评论池)设置偏好，或执行 `weibo-exp run --dry-run` 预览任务。

### 4. 选择运行方式

评论和转发创建成功后，默认等待 6–9 秒再请求删除；转发时关闭“同时评论”。

**按需手动启动**：执行一次 `run`，输入 `yes` 确认后，程序自动完成本次签到、评论和转发任务。

```sh
weibo-exp run
```

**每天自动执行（可选）**：希望电脑每天自行运行时，再启用定时任务。

```sh
weibo-exp schedule install
```

首次使用默认每天 **10:00** 执行，之后每 **4 小时** 检查补跑，至当天结束，即 **10:00、14:00、18:00、22:00**。自定义时间同样从设定时间开始计算，例如 09:30 对应 09:30、13:30、17:30、21:30。设置时间可执行 `weibo-exp schedule install --at 09:30`；省略 `--at` 时沿用已保存的时间。

安装、重新加载调度或系统恢复可用时，还可能触发一次检查。程序到达当天设定时间后执行剩余任务；已有待清理内容会优先处理。

设置好定时任务后可以关闭终端。执行时电脑需处于开机、唤醒、联网状态；Windows 还需保持安装时的用户登录。仅使用手动运行的用户，完成安装和登录即可。

<details>
<summary>不同系统的后台运行、睡眠与补跑说明</summary>

锁屏或关闭显示器后，只要系统仍保持唤醒、运行任务的用户环境可用，就能继续执行。程序采用电脑的本地时区；调度可能稍有延迟，可通过 `weibo-exp logs` 查看实际启动时间。

| 系统 | 自动运行条件与补跑行为 |
| --- | --- |
| macOS | 使用当前用户的 launchd，保持该用户登录。睡眠期间错过的日历触发会在唤醒后合并补触发一次；登录加载任务时也会检查。MacBook 合盖通常进入睡眠，接通电源不等于系统保持唤醒。 |
| Windows | 使用任务计划程序，仅在安装任务的用户登录期间运行，锁屏可继续。已开启“错过后尽快运行”；恢复可用后的补触发可能有延迟，可查看日志或手动执行 `run`。 |
| Linux | 使用用户级 systemd timer，要求 `systemctl --user` 可用且用户管理器持续运行。日历定时器在唤醒后补触发；已开启持久化，重新激活时会检查停用期间错过的时刻。默认计时精度为 1 分钟，10:00 后数十秒启动属于正常情况。 |
| WSL 2 | 除 Linux 条件外，还需 Windows 主机唤醒且 WSL 发行版运行。systemd 服务本身不会保证 WSL 一直存活；WSL 已关闭时，Linux 定时器无法启动它。日常 Windows 自动运行可直接使用 Windows 版。 |

### 插电时保持电脑唤醒

**Windows 11**

1. 打开「设置 → 系统 → 电源和电池」。
2. 展开「屏幕、睡眠和休眠超时」。
3. 在「已接通电源」下，将「使我的设备在以下时间后进入睡眠状态」设为「从不」。
4. 关闭屏幕的时间按个人习惯设置，使用电池时的设置可以保持原样。

**macOS**

1. 打开「系统设置 → 电池 → 选项」。
2. 开启「使用电源适配器供电且显示器关闭时，防止自动进入睡眠」。
3. 运行期间保持连接电源；关闭显示器或锁屏即可，无需一直亮屏。

不同系统版本和机型的选项名称、位置可能略有不同。这些设置针对自动睡眠，笔记本合盖或手动选择睡眠仍可能暂停任务；程序未配置定时唤醒电脑。

关机期间无法执行。恢复后运行的是**当天剩余任务**，不会补做之前日期的互动；到达当天设定时间前只处理已有待清理内容。网络异常或临时失败由后续每 4 小时检查补跑，也可随时执行 `weibo-exp run`。

参考：[Apple 调度与睡眠](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/ScheduledJobs.html)、[Windows 错过时间后的调度](https://learn.microsoft.com/en-us/windows/win32/taskschd/tasksettings-startwhenavailable)、[systemd 定时器手册](https://www.man7.org/linux/man-pages/man5/systemd.timer.5.html)、[WSL 的 systemd 与运行生命周期](https://learn.microsoft.com/en-us/windows/wsl/systemd)。

</details>

查看运行情况：

| 想做什么 | 命令 |
| --- | --- |
| 查看今日进度 | `weibo-exp status` |
| 查看定时设置 | `weibo-exp schedule status` |
| 查看定时运行记录 | `weibo-exp logs` |

`status` 显示账户和今日任务进度，例如 `今日任务：已完成 3/3 个超话`。签到状态实时读取微博；评论和转发进度依据本机数据目录中的记录。手机 App 或其他工具完成的互动，请结合 App 中的额度自行调整任务数量。

`status` 和 `config show` 同时显示每日执行时间与定时任务安装状态。任务属于其他数据目录时会提示；详细设置可执行 `weibo-exp schedule status` 查看。尚未安装时，执行 `weibo-exp schedule install` 开启自动运行。

## 日常使用

以下命令适用于 macOS、Windows PowerShell 和 Linux，在任意目录执行即可。每行是一条独立命令，可按需选择。

### 查看进度或立即执行

先查看进度或预览剩余任务：

| 想做什么 | 命令 |
| --- | --- |
| 查看今日进度 | `weibo-exp status` |
| 查看各超话详情 | `weibo-exp topics` |
| 预览剩余任务及预计数量 | `weibo-exp run --dry-run` |

立即执行会发布并删除评论和转发，输入 `yes` 确认后开始：

```sh
weibo-exp run
```

### 选择超话

先使用上表的“查看各超话详情”命令获取列表，例如：

```text
[1] 示例甲超话
[2] 示例乙超话
[3] 示例丙超话
```

按需选择一种设置方式：

| 任务范围 | 命令 |
| --- | --- |
| 当前列表中的第 1、3 个 | `weibo-exp config set --topics "1,3"` |
| 指定名称 | `weibo-exp config set --topics "示例甲超话,示例丙超话"` |
| 全部关注超话 | `weibo-exp config set --topics all` |

配置时会重新获取关注列表，并把所选超话的 ID 和名称保存下来；后续顺序变化不会改变已保存的选择。若刚刚新增或取消关注，可先重新执行 `weibo-exp topics`，也可直接按名称选择。使用“全部关注超话”时，新关注的超话会在后续检查中自动加入。

### 调整任务数量

按微博 App 中显示的每日任务额度设置。例如，将评论与转发分别调整为 6 条和 3 条：

```sh
weibo-exp config set --comment-limit 6 --repost-limit 3
```

评论和转发数量分别支持 **0–100**。填 `0` 可关闭对应互动；只保留签到可执行：

```sh
weibo-exp config set --comment-limit 0 --repost-limit 0
```

提高数量后，程序会结合当天的本地进度补齐差额。评论和转发使用不同的候选帖子，并排除本人发布和本工具今日已互动的帖子；可用帖子不足时会报告未完成项。

### 设置执行时间

```sh
weibo-exp config set --time 10:00
```

时间采用电脑本地时区和 24 小时制，设置后会同步已安装的定时任务。每天到点执行，此后每 4 小时检查补跑，至当天结束；当天已完成的互动会自动跳过。

暂停或恢复自动执行：

| 操作 | 命令 |
| --- | --- |
| 暂停 | `weibo-exp schedule uninstall` |
| 恢复 | `weibo-exp schedule install` |

### 查看日志

| 查看内容 | 命令 |
| --- | --- |
| 最近运行记录 | `weibo-exp logs` |
| 每份日志最后 20 行 | `weibo-exp logs --lines 20` |
| 错误日志 | `weibo-exp logs --errors` |

自动任务的记录带有时间戳，便于确认执行时间和结果。

日志默认保留最近 7 天，在每次定时任务启动时清理过期记录。调整为 14 天或手动清空：

| 操作 | 命令 |
| --- | --- |
| 保留最近 14 天 | `weibo-exp config set --log-retention-days 14` |
| 手动清空 | `weibo-exp logs clear` |

`logs` 默认显示普通日志和错误日志各自最后 100 行。`logs clear` 会要求确认；保留天数设为 `0` 时关闭自动清理。修改保留天数后，下次定时启动时应用新规则。

## 评论池

所有超话共用一份评论池，每次按日期、超话和原帖选取一条内容；同一组合对应同一条模板，不保证每条评论都不同。内置 10 条：

```text
打卡
来啦
tt
TT
[打call]
[打call][打call][打call]
[哇]
[哇][哇][哇]
[送花花]
[期待]
```

查看当前评论池和其他设置：

```sh
weibo-exp config show
```

### 自定义评论

1. 创建一个纯文本文件。macOS 使用“文本编辑”，先选择“格式 → 制作纯文本”；Windows 使用记事本。
2. 每行填写一条评论，可参考下面的例子。添加、修改或删除对应行，保留至少一条内容。
3. 以 UTF-8 编码保存，文件名填写 `comments.txt`，确认完整文件名只有一个 `.txt` 后缀。可保存到桌面或其他方便找到的位置。

```text
来啦
tt
[打call]
```

在终端输入 `weibo-exp config set --templates-file `（末尾留一个空格），把文件拖入终端填入路径，按回车导入。也可手动填写完整路径；路径含空格时请加引号。然后查看结果：

```sh
weibo-exp config show
```

导入会替换当前评论池，自动忽略空行、去除行首尾空白并合并重复内容。之后修改文件，再次导入即可生效；导入完成后无需保留文件供程序读取。

普通文字、emoji 和微博表情写法（如 `[打call]`）均可作为评论内容。

## 命令列表

在程序后加上命令使用，例如 `weibo-exp status`。

| 命令 | 用途 |
| --- | --- |
| `install` | 安装或升级程序，并配置用户 PATH |
| `uninstall` | 卸载程序及运行数据 |
| `login` | 使用微博 App 扫码登录 |
| `status` | 查看账户与今日任务进度 |
| `topics` | 查看关注超话、任务范围及完成情况 |
| `run --dry-run` | 预览今日剩余任务 |
| `run` | 立即执行今日剩余任务 |
| `config show` | 查看配置与评论池 |
| `config set` | 修改超话、数量、时间和评论池等设置 |
| `config path` | 显示配置文件位置 |
| `config reset` | 恢复全部默认设置，包含评论池和执行时间 |
| `schedule install` | 启用每日自动执行 |
| `schedule status` | 查看定时任务设置 |
| `schedule uninstall` | 停止每日自动执行，保留程序和数据 |
| `logs` / `logs clear` | 查看 / 清空运行日志 |
| `cleanup` | 查看待清理的评论、转发记录 |
| `cleanup confirm` | 人工核对后移除指定待清理记录 |
| `help` / `version` | 查看帮助 / 程序版本 |

参数说明可通过 `weibo-exp install --help`、`weibo-exp config set --help` 查看。`status`、`topics`、`config show` 支持 `--json`，便于与其他工具配合。

`run --yes`、`install --yes`、`uninstall --yes` 和 `logs clear --yes` 可跳过各自的确认。`config reset` 会直接恢复默认配置，登录会话与任务进度保留。全局参数（如 `--data-dir`）放在命令前，命令选项放在命令后。

## 安装选项

默认安装位置：

| 系统 | 程序目录 |
| --- | --- |
| macOS | `~/Applications/weibo-exp` |
| Windows | `%LOCALAPPDATA%\Programs\weibo-exp` |
| Linux | `~/.local/share/weibo-exp` |

在解压目录中可选择自定义位置（Windows PowerShell 将 `./weibo-exp` 换为 `.\weibo-exp.exe`）：

```bash
# 自定义安装目录
./weibo-exp install --dir "/你的安装目录"
```

默认位置和自定义位置都会配置 PATH。自动配置支持 macOS / Linux 的 zsh、bash、fish，以及 Windows 的用户环境变量。安装时若发现其他位置已有同名命令，会显示路径供你处理。

希望自行管理命令入口时，可使用 `./weibo-exp install --no-path`。该选项跳过 PATH 设置，升级时保留本工具之前添加的设置。使用这种方式且尚未配置 PATH 时，进入安装目录使用 `./weibo-exp`（Windows 为 `.\weibo-exp.exe`），或填写程序的完整路径。

<details>
<summary>自定义数据目录</summary>

配置、登录会话和任务进度保存在用户数据目录。可在安装时指定位置；当该位置不同于默认数据目录时，定时日志也保存在其 `logs` 子目录：

```bash
./weibo-exp --data-dir "/你的数据目录" install --dir "/你的安装目录"
```

安装后的程序会自动找到这份数据。以后从新下载的程序升级时，带上相同的 `--data-dir` 即可。每个系统用户使用一份原生定时任务。

也可以通过环境变量 `WEIBO_EXP_DATA_DIR` 指定数据目录，其日志位置与 `--data-dir` 一致。显式 `--data-dir` 优先于环境变量。切换定时任务的数据目录时，`schedule install` 会显示旧、新位置；`schedule uninstall` 只卸载属于当前数据目录的任务。

`--dir` 选择程序安装位置，`--data-dir` 选择运行数据位置，两者可以独立设置。已有数据和日志不会自动搬迁；若更换数据位置并使用定时任务，请执行 `weibo-exp schedule install`。

</details>

## 文件存储位置

程序、运行数据和定时日志分别存放。通常只需使用 `config`、`logs` 命令管理，备份或排查时可参考以下默认位置：

| 系统 | 配置与运行数据目录 | 定时日志目录 |
| --- | --- | --- |
| macOS | `~/Library/Application Support/weibo-exp` | `~/Library/Logs/weibo-exp` |
| Windows | `%APPDATA%\weibo-exp` | `%LOCALAPPDATA%\weibo-exp` |
| Linux | `~/.config/weibo-exp` | `~/.cache/weibo-exp` |

`~` 表示当前用户的主目录；Windows 的 `%APPDATA%`、`%LOCALAPPDATA%` 可粘贴到文件资源管理器地址栏中访问。Linux 设置了 `XDG_CONFIG_HOME` 或 `XDG_CACHE_HOME` 时，分别使用对应目录替代 `~/.config` 或 `~/.cache`。

<details>
<summary>文件用途、系统调度与 PATH 配置位置</summary>

数据目录中的文件按需生成：

| 文件或目录 | 内容 |
| --- | --- |
| `config.json` | 超话选择、每日时间、互动数量、评论池及日志保留天数 |
| `session.json` | 微博登录会话，备份和分享时请妥善保护 |
| `state.json` | 互动进度及待清理记录；保存时保留最近 7 天互动历史，待清理记录单独保留 |
| `installation.json` | 安装位置与卸载所需记录 |
| `run.lock` | 防止多个任务同时操作的锁文件 |
| `login-tmp/` | 扫码图片的临时存放目录，正常登录流程结束后清理图片 |

程序目录还包含 `.weibo-exp-install.json`，用于定位数据及确认程序归属。两份安装记录也保存本工具添加的 PATH 设置，供升级与卸载使用。评论池保存在 `config.json` 中；导入用的 `comments.txt` 可由用户自行保存。

日志目录包含 `scheduled.log` 和 `scheduled-error.log`，分别记录定时任务输出与错误。手动执行 `run` 的输出显示在终端中。使用 `config path` 查看实际配置路径，使用 `logs` 查看实际日志路径与最近记录。

系统调度配置由 `schedule` 命令管理，其位置独立于程序与数据目录：

- **macOS**：`~/Library/LaunchAgents/com.surrealgit.weibo-exp.plist`。
- **Linux**：`~/.config/systemd/user/weibo-exp.service` 与 `.timer`；设置 `XDG_CONFIG_HOME` 时使用其下的 `systemd/user`。
- **Windows**：任务计划程序中的 `weibo-exp` 任务。

PATH 设置的位置：

- **zsh**：`~/.zshrc`；设置 `ZDOTDIR` 时使用该目录下的 `.zshrc`。
- **bash**：`~/.bashrc` 和登录配置文件（按顺序选择已有的 `.bash_profile`、`.bash_login`、`.profile`，均不存在时创建 `.profile`）。
- **fish**：`~/.config/fish/conf.d/weibo-exp.fish`；设置 `XDG_CONFIG_HOME` 时使用对应目录。
- **Windows**：当前用户注册表 `HKEY_CURRENT_USER\Environment` 下的 `Path`。

shell 配置中的新增段带有 `weibo-exp PATH` 标记。升级复用已有记录；卸载撤销本工具添加的段或条目，保留用户原有内容。

</details>

## 升级与卸载

### 升级

从[发行页面](https://github.com/SurrealGit/weibo-exp/releases/latest)下载新版并解压，在新版文件夹打开终端，再次执行安装命令。程序会使用已有安装位置，保留配置、评论池、登录会话和任务进度。

| macOS / Linux | Windows PowerShell |
| --- | --- |
| `./weibo-exp install` | `.\weibo-exp.exe install` |

同一目录升级可沿用原定时设置；若程序路径发生变化，请执行 `weibo-exp schedule install` 更新定时任务。

### 卸载

通过 `install` 安装的程序，可在任意目录执行：

```sh
weibo-exp uninstall
```

核对清理范围后输入 `yes`。默认删除已安装程序、定时任务、配置、登录会话、任务进度、日志及工具临时文件，并撤销本工具添加的 PATH 设置。数据删除不可恢复。

若准备以后继续使用，可以保留配置、登录会话和任务进度：

```sh
weibo-exp uninstall --keep-config
```

使用 `--keep-config` 时仍会清理日志、定时任务和本工具添加的 PATH 设置。存在待删除或待人工检查的内容时，先按 `weibo-exp cleanup` 的提示处理，再卸载。卸载结束后请重新打开终端。

下载的压缩包、解压副本和自己创建的评论文本可按需自行清理。Windows 卸载时请等待终端显示“后台卸载清理完成”。

## 常见问题

**什么时候可以关闭终端？**

手动执行 `run` 时，等待本次任务结束再关闭；启用定时任务后可以关闭终端。各平台的后台运行条件见[选择运行方式](#4-选择运行方式)中的折叠说明。

**错过执行时间怎么办？**

恢复可用后执行 `weibo-exp logs` 查看补跑记录，或执行 `weibo-exp run` 立即完成当天剩余任务。手动运行不受每日设定时间限制；各平台补跑行为见[选择运行方式](#4-选择运行方式)。

**命令提示找不到程序怎么办？**

安装完成后，关闭并重新打开终端；若仍未生效，请完全退出终端应用后重新打开。使用 `--no-path` 安装的用户，可进入安装目录运行 `./weibo-exp`（Windows 为 `.\weibo-exp.exe`），或自行配置 PATH。

Windows 用户若此前可以运行、后来突然找不到命令，请检查「Windows 安全中心 → 病毒和威胁防护 → 保护历史记录」，确认程序是否被隔离。若有相关记录，请通过 [Issues](https://github.com/SurrealGit/weibo-exp/issues) 反馈检测名称和所用发行版本；分享截图时请遮盖个人信息。

**二维码在哪里？登录过期了怎么办？**

执行以下命令重新扫码。程序会尝试打开二维码图片，终端同时显示图片路径。图片保存在数据目录的 `login-tmp` 下，正常登录流程结束后清理。

```sh
weibo-exp login
```

自动打开失败时，可按终端显示的路径手动打开图片。也可使用 `weibo-exp login --no-open` 只输出路径；默认扫码等待 4 分钟，`--timeout 8m` 可延长至 8 分钟。

**显示待清理内容怎么办？**

先执行 `weibo-exp cleanup` 查看记录，再执行 `weibo-exp run` 重试删除已知 ID 的内容；清理完成后会继续当天剩余任务。遇到需要人工检查的记录，在微博中找到对应评论或转发，确认已删除后，按照终端提示执行 `weibo-exp cleanup confirm` 更新本地记录。该确认命令只更新本地记录，实际内容需先在微博中清理。

发布请求遇到响应丢失等情况时，程序会保留待检查记录并暂停后续互动。请先在微博中检查原帖，删除可能已发布的内容，确认内容不存在后再清除记录；结果未知的操作不计入已完成数量。

**运行成功，经验是多少？**

计分规则、每日经验测算及 App 查看方式见[经验值与超 LIKE](#经验值与超-like)。

使用时请遵守微博平台规则，关注发布内容及执行频率；遇到验证或账号限制，先在微博 App 中处理。登录会话保存在本机，分享日志或反馈问题时请保护账号凭证。

**候选帖子不足怎么办？**

可先执行 `weibo-exp config set --max-feed-pages 16` 增加读取页数，再使用 `weibo-exp run --dry-run` 预览。默认最多读取 8 页；能否完成目标仍取决于超话中的可用帖子数量。

## 开发与构建

使用 Go 1.23 或更高版本，在仓库根目录执行：

```bash
go test ./...
go build -trimpath -ldflags="-s -w" -o dist/weibo-exp ./cmd/weibo-exp
```

完整检查与发行构建（在 macOS / Linux 或提供 POSIX shell 的构建环境中运行）：

```bash
./scripts/check.sh
./scripts/build-release.sh
./scripts/test-release.sh
```

- `check.sh`：格式检查、静态检查、随机顺序测试、竞态测试及本机构建。
- `build-release.sh`：构建四个平台/架构的程序，生成压缩包与 SHA-256 校验文件。
- `test-release.sh`：验证压缩包清单、校验和及本机入口。

发行脚本使用 Go、tar、zip、shasum，验证脚本另使用 unzip。产物位于 `dist/releases/`；`dist/weibo-exp` 为本机入口，带平台后缀的程序用于分平台构建。

## 许可证

本项目采用 [MIT License](LICENSE)。README 徽章使用的 Lucide／Feather 图标及其许可文本见 [第三方声明](THIRD_PARTY_NOTICES.md)。
