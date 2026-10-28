# iLink 协议同步：上游 openclaw-weixin 2.4.10 → openilink-hub

本文记录一次针对 iLink 协议的上游比对结果，以及落到本仓的实际改动。逐条给出「上游是什么 / 本仓现状 / 是否合并 / 为什么」。

## 比对来源

| 来源 | 版本/位置 |
| --- | --- |
| 上游插件 | `Tencent/openclaw-weixin` @ `874637a`，`package.json` version `2.4.10`（CHANGELOG 含 Unreleased 段） |
| 上游协议文档 | `docs/protocol_zh_CN.md`（与 `docs/protocol.md` 同源） |
| 上游变更记录 | `CHANGELOG.md`（2.1.2 → 2.4.10） |
| SDK | `github.com/openilink/openilink-sdk-go v0.6.0`（`go list -m -versions` 确认这是最新发布版，**未 fork/vendor，未改 go.mod 依赖版本**） |
| 本仓实现 | mock server：`internal/provider/ilink/mockserver/**`；真实 provider：`internal/provider/ilink/{ilink,bind}.go` |

本仓范围内所有 `encoding/json` 调用点已统一换成 `internal/jsonx`（v1 语义的 `encoding/json/v2` 包装），不改变线格式。

## 结论速览

| 必查项 | 上游要求 | 本仓处理 |
| --- | --- | --- |
| getUpdates 游标 | 回传 `get_updates_buf`；`sync_buf` 已废弃；`longpolling_timeout_ms` 为建议超时 | ✅ 合并：mock 推进并返回新游标、保留 `sync_buf`、可注入 `longpolling_timeout_ms` |
| get_bot_qrcode | `POST ?bot_type=3` + `{local_token_list}` | ✅ 合并：GET/POST 双支持、解析 `local_token_list`、命中已绑定 token → `binded_redirect` |
| 二维码状态机 | wait / scaned / confirmed / expired / need_verifycode / verify_code_blocked / scaned_but_redirect / binded_redirect | ✅ mock 全量支持；provider bind 侧合并 `scaned_but_redirect`，验证码/已绑定 UI 语义未合并（见下） |
| sendMessage | `context_token`、`run_id`、`base_info`、非零 `ret`、请求头 | ✅ mock 记录 `run_id`/`base_info`/请求头、`to_user_id` 缺失返回 `ret:-1`；SDK 不解析 `ret`（不一致清单 7） |
| 消息模型 | 11/12 tool call、`ref_msg`、voice 转写、媒体字段与 aeskey 优先级 | ✅ `ref_msg`/voice 转写/媒体字段/`raw_items` 透传合并；11/12 到 provider 的映射与 aeskey 优先级未合并（跨模块/SDK 限制） |
| CDN | getuploadurl 字段、上传 `x-encrypted-param` 判定与重试、下载 `full_url` 回退 + AES-128-ECB | ✅ 记录上传请求、支持 `upload_full_url`/`full_url`、可注入 CDN 失败状态 |

---

## 1. getUpdates：游标字段与长轮询超时

**上游**（`protocol_zh_CN.md` L166-L206）：请求发 `get_updates_buf`；响应含 `ret` / `msgs` / `get_updates_buf`（下次回传的游标）/ `longpolling_timeout_ms`（服务端建议的下次超时）。`sync_buf` 只在类型定义里保留并标废弃，客户端不会回退读取它；只有返回的 `get_updates_buf` 非空时才保存游标。

**本仓现状（改前）**：`mockserver/http_sdk.go` 的 `getUpdates` 响应把新游标写进了废弃字段 `sync_buf`，而 `get_updates_buf` 原样回显请求值 —— 等于游标永远不推进；`longpolling_timeout_ms` 完全缺失。SDK 的 `Monitor`（`monitor.go` L117-L123）只认 `GetUpdatesBuf`，因此本仓真实 provider 持久化的 `sync_buf` 一直是"把发出去的 buf 存回来"。

**是否合并：是。**

- 响应 `get_updates_buf` 改为服务端新游标（引擎游标为空时才回退请求值）；
- 保留 `sync_buf` 输出（兼容仍读旧字段的客户端，同时保持本仓 mock provider `OnSyncUpdate` 的 `sync_buf` 语义不变）；
- 新增 `WithLongPollingTimeout(d)` 选项；默认不发该字段（客户端用自己的默认值，避免改变既有测试时序）。

**不合并**：真正的游标过滤/重放语义。mock 用 channel 投递消息、没有持久消息日志，无法按游标重放；要做得先有消息日志，收益低。

## 2. get_bot_qrcode：POST 表单 vs SDK 的 GET

**上游**（L88-L111）：`POST /ilink/bot/get_bot_qrcode?bot_type=3`，body `{"local_token_list": []}`；客户端最多带最近 10 个本地 bot token，让服务端识别"已绑定"并回 `binded_redirect`。

**SDK v0.6.0**（`auth.go` L18-L38）：`GET /ilink/bot/get_bot_qrcode?bot_type=<DefaultBotType=3>`，没有 body，不发 `local_token_list`。

**本仓现状（改前）**：mock 只注册了 `GET`，POST 会 405；`qrcode_img_content` 返回的是 `base64(QRContent)`。

**是否合并：是。**

- 同时注册 `GET` 与 `POST`（Go 1.22+ ServeMux 分方法注册），POST 解析可选的 `local_token_list`（空 body 也接受）；空 POST body 触发 `io.EOF` 被显式容忍；
- 引擎记住 `method` / `bot_type` / `local_token_list`（`Engine.LastQRRequest()`），测试可直接断言；
- 命中"已绑定"token（`ConfirmQR` 会记住返回的 `bot_token`，另有 `MarkBoundToken` / `POST /mock/qr/bind`）时，新会话直接进入 `binded_redirect`；
- `qrcode_img_content` 改为返回 QR 内容本身（`https://mock.ilink/qr/<code>`）。上游对该字段的说明是"用于展示二维码的 URL 或内容"，而本仓前端 `QrCanvas` 是把该字符串编码成二维码（`web/src/pages/bots.tsx`），旧实现的 base64 会被整段当成二维码内容编码，语义错误。

## 3. 二维码状态机

**上游**（L137-L148）：8 个状态 —— `wait` / `scaned` / `confirmed` / `expired` / `need_verifycode` / `verify_code_blocked` / `scaned_but_redirect`（带 `redirect_host`）/ `binded_redirect`。需要验证码时客户端追加 `verify_code` 查询参数（L119-L123）。

**SDK v0.6.0**（`auth.go` L126-L179）：`LoginWithQR` 只处理 `wait` / `scaned` / `scaned_but_redirect` / `expired` / `confirmed`；`need_verifycode` / `verify_code_blocked` / `binded_redirect` 没有分支，会落空并继续轮询到 8 分钟登录超时；`PollQRStatus` 也没有 `verify_code` 参数。

**本仓现状（改前）**：mock 只有 `wait` / `scanned`→`scaned` / `confirmed` / `expired`；`internal/provider/ilink/bind.go` 对 `scaned_but_redirect` 没有任何处理（状态被当作未知状态透传，继续轮询原 host，直到二维码过期）。

**是否合并：部分合并。**

已合并：

- mock 补齐 4 个状态，新增控制端点 `POST /mock/qr/state`（`{"status","redirect_host","verify_code"}`）可强制任意状态；
- `GET /ilink/bot/get_qrcode_status` 接受 `verify_code`：验证码缺失时立即返回 `need_verifycode`（不阻塞，客户端才知道要弹输入框）；验证码正确 → 有凭证则 `confirmed`、无凭证则 `scaned`；错误 → `verify_code_blocked`（终态）；会话未设置期望验证码时接受任意非空验证码；
- `scaned_but_redirect` 设计成一次性通知：返回 `redirect_host` 后会话回到 `scanned`（模拟客户端换 host 继续轮询），避免重复返回同一状态导致死循环；
- `binded_redirect` / `verify_code_blocked` / `expired` 为立即返回的终态；
- provider 侧 `bind.go` 合并 `scaned_but_redirect`：用 `redirectPollBase()` 切到 `https://<redirect_host>` 继续轮询（`PollQRStatus` 的 variadic baseURL 参数），刷新二维码时复位（`bind_test.go` 覆盖）。

**不合并**（需要超出本任务写范围的改动）：

- `binded_redirect` 的"已绑定 = 登录成功"语义：上游在 2.4.3 把它当成成功空操作；本仓要落地需 `internal/api` 与前端识别该状态，`bind.go` 没有可注入 base URL 的测试接缝（`ilinkSDK.NewClient("")` 写死默认地址），单独改会引入未验证行为；
- `need_verifycode` / `verify_code_blocked` 的交互闭环：上游用 stdin 提示输入验证码；本仓需要 API + WebUI 增加验证码输入通道，属其他同学范围。

## 4. sendMessage、base_info 与请求头

**上游**（L208-L248、L34-L65）：`msg` 携带 `context_token`、`client_id`、`message_type/state`、`item_list`，传入 `run_id` 时一并携带；`base_info` 必有 `channel_version` + `bot_agent`；响应 `{ret, errmsg}`，非零 `ret` 抛错；请求头 `Content-Type` / `AuthorizationType: ilink_bot_token` / `Authorization: Bearer <token>` / `X-WECHAT-UIN`（随机 uint32 十进制再 base64）/ `iLink-App-Id: bot` / `iLink-App-ClientVersion`（`0x00MMNNPP` 十进制）/ 可选 `SKRouteTag`。

**本仓现状（改前）**：mock 只回 `{"ret":0}`；`msg.run_id` 与 `base_info` 被丢弃（SDK 类型里也没有库位）；不记录任何请求头；`to_user_id` 缺失时仍按成功处理。

**是否合并：是（mock 侧）。**

- 新增 `SendMessageWithMeta` / `SendMeta`，从原始 body 里解析 `msg.run_id` 与 `base_info` 并记录到 `SentMessage`（`/mock/sent` 可见）；
- `to_user_id` 为空时按上游风格返回 HTTP 200 + `{"ret":-1,"errmsg":"missing to_user_id"}`，且不记为已发送；
- `HTTPServer` 增加中间件，把最近一次 `/ilink/` 请求头录到 `Engine.RequestHeaders()`；测试断言 `iLink-App-Id=bot`、`AuthorizationType`、`Authorization`、`SKRouteTag`、`Content-Type`，并验证 `X-WECHAT-UIN` 是"十进制 uint32 的 base64"；
- 另有一条测试断言二维码请求只带应用头、不带 `AuthorizationType`/`Authorization`/`X-WECHAT-UIN`（上游 L48 的客户端行为）。

**不合并 / 取舍**：

- 引擎内部错误仍返回 HTTP 500（协议外的行为），保留"硬错误让 SDK 立刻失败"；业务校验走 `ret`。原因是 SDK v0.6.0 不解析 `ret`（见不一致清单 7），全部改成 200+ret 会让 SDK 使用者静默丢消息；
- `bot_agent` 只记录不校验：SDK 的 `BaseInfo` 没有该字段，mock 无法从 SDK 收到它（不一致清单 2）。

## 5. 消息模型

### 5.1 MessageItem type 11/12（tool_call_start / tool_call_result）

**上游**：`MessageItemType.TOOL_CALL_START = 11`、`TOOL_CALL_RESULT = 12`，对应 `tool_call_start_item`（`tool_name`/`tool_call_id`）与 `tool_call_result_item`（多一个 `status`）。
**SDK v0.6.0**：`MessageItem` 只有 1/2/3/4/5，没有 11/12 的字段，`ilink.MessageItem` 无法承载。
**是否合并：部分合并。** mock 新增 `InboundRequest.raw_items`（原始 JSON 片段，按 message_id 存放），`getupdates` 响应的 `item_list` 里原样拼接（响应体用 `apiMessage` 包装：嵌入 SDK 消息并覆盖 `item_list` 为 `[]jsonx.RawMessage`），配套测试断言 11/12 往返不变。**不合并**的是映射到 `provider.MessageItem`：本仓 `internal/provider/provider.go` 没有 tool call 类型，属跨模块改动，且 SDK 解析层也拿不到。

### 5.2 ref_msg 引用

**上游**：`ref_msg` 表示被引用消息，含可选 `title` 与 `message_item`。
**本仓现状（改前）**：真实 provider 的 `convertItem` 已支持 `RefMsg`，但 mock 无法注入（`ItemRequest` 没有引用字段），该分支实际上测不到。
**是否合并：是。** `ItemRequest` 新增 `ref`（嵌套 item）与 `ref_title`，`convertItemRequest` 生成 `ref_msg`；新增端到端测试验证 `provider.RefMsg.Title` / `RefMsg.Item.Text`。

### 5.3 voice_item 转写与音频字段

**上游**：`voice_item.text` 是语音转写文本；另有 `encode_type` / `sample_rate` / `playtime`。
**是否合并：是。** `ItemRequest.Text` 现在用于 voice 的转写文本，另加 `playtime` / `sample_rate` / `encode_type` / `bits_per_sample`；测试验证转写文本与 `play_time` 传到 `provider.Media.PlayTime`。

### 5.4 媒体字段与 aeskey 优先级

**上游**（L420-L433）：`image_item.aeskey` 是 32 位十六进制入站密钥，**优先于** `media.aes_key`；`media.aes_key` 在本仓/SDK 语境里是 `base64(hex字符串)`；图片发 `mid_size`，视频发 `video_size`，文件 `len` 是十进制明文字节数。

**是否合并：是（发送端字段与格式）。** 注入媒体的 `media.aes_key` 从 `base64(原始16字节)` 改为 `base64(hex)`（旧格式 SDK 也能解，但不符合上游发送端行为）；image 额外带 `image_item.aeskey`（hex）与 `mid_size`，video 带 `video_size`/`video_md5`，file 带 `len`/`md5`，并支持 `image_item.url`。**不合并**的是"`image_item.aeskey` 优先"这条客户端行为：SDK 只读 `media.aes_key`（不一致清单 8）。

**另发现一处本仓 bug（不在本任务写范围，未改）**：`internal/provider/ilink/ilink.go` 的 `convertItem` 用 `image_item.url` 覆盖了 `media.full_url`（两者都被塞进 `provider.Media.URL` 一个字段），会让"带 `image_item.url` 的图片"在下载时指向展示地址。修法需要给 `provider.Media` 拆字段（`internal/provider/provider.go`），建议单独排期。

### 5.5 message_type 默认值

**上游**：`message_type` 1=用户、2=Bot。
**是否合并：是。** mock 之前不设置该字段（JSON 省略 → 0）；现在默认 1，允许显式指定。

## 6. CDN：getuploadurl、上传、下载

### 6.1 getuploadurl 请求/响应字段

**上游**（L250-L299）：请求 `filekey` / `media_type` / `to_user_id` / `rawsize` / `rawfilemd5` / `filesize`（填充后密文大小）/ `no_need_thumb` / `aeskey`（hex），需要缩略图时才有 `thumb_rawsize` / `thumb_rawfilemd5` / `thumb_filesize`；响应 `upload_param` / `thumb_upload_param` / `upload_full_url`，客户端优先 `upload_full_url`。
**是否合并：是（主体字段）。** mock 记录完整请求（`Engine.LastUploadRequest()`），测试断言 `no_need_thumb=true`、`rawsize`、`filesize == AESECBPaddedSize(rawsize)`、`rawfilemd5`、`media_type`、`aeskey` 为 32 位 hex；配置 CDN base（`SetCDNBaseURL`）后返回 `upload_full_url`，并有一条测试用"客户端 CDN base 指向错误地址"来证明上传确实走了 `upload_full_url`。
**不合并**：缩略图字段与 `thumb_upload_param`（SDK 固定 `no_need_thumb: true` 且不消费缩略图，加了也无人验证）；`filesize/rawsize` 一致性校验（可选的服务端校验，收益低、易误伤）。

### 6.2 上传判定与重试

**上游**（L469）：成功 = HTTP 200 且 `x-encrypted-param` 非空；4xx 立即终止；其他失败（含缺响应头）最多 3 次。
**是否合并：是（可注入失败）。** mock 本来已回 `x-encrypted-param`；新增 `SetCDNUploadStatus(status)` 注入失败，测试验证 4xx 时 SDK 立即失败、不进入 2s 重试退避（`elapsed < 1s`）。500 的三次重试未加测试：SDK 的重试间隔是固定的 2s，验证会拖慢单测，留待需要时用 `-timeout` 放宽。

### 6.3 下载 full_url 回退与 AES-128-ECB 解密

**上游**（L437-L476）：媒体引用优先 `full_url`，否则拼 `<cdn_base>/download?encrypted_query_param=...`；解密 AES-128-ECB + PKCS#7。
**是否合并：是。** `SetCDNBaseURL` 后 mock 在 `CDNMedia` 上带 `full_url`；新增端到端测试证明真实 provider（其 `client` 保留生产 CDN base，不接受注入）**仅靠 `full_url`** 就能从 mock 下载并解密成功 —— 改前做不到，旧的 `integration_test.go` 只能写注释绕过 HTTP 层直接调 engine。
**不合并**：`encrypt_type` 的细分语义。SDK 把 `1` 定义为 AES-128-ECB，上游注释里 `0/1` 另表示"只加密 fileid / 打包缩略图中图"，命名冲突但无可验证的差异行为，不在 mock 中建模。

## 7. 上游 CHANGELOG（2.1.2 → 2.4.10）逐条可合并性

| 版本 | 上游变更 | 结论 |
| --- | --- | --- |
| 2.4.10 / 2.4.9 / 2.4.7 / 2.4.2 / 2.4.1 | OpenClaw 宿主 Plugin SDK 兼容、undici/Node24、npm 打包预编译产物、配置重载 | 不合并：插件宿主/打包主题，与 iLink HTTP 协议无关 |
| 2.4.6 | sendMessage 响应校验（非零 `ret` 抛错）、`SESSION_EXPIRED_ERRCODE`→`STALE_TOKEN_ERRCODE`、`classifyFetchError`、日志脱敏 | 部分相关：`-14` 语义 SDK/hub 已一致；`ret` 校验 SDK 缺失（不一致清单 7）；网络错误分类属插件侧日志能力，不合并 |
| 2.4.4 | tool_call 进度消息、getUpdates 支持外部 AbortSignal | 11/12 见 5.1；SDK 的 `GetUpdates` 已支持 context 取消（等价能力），不合并 |
| 2.4.3 | 修复空的 `iLink-App-Id`/`iLink-App-ClientVersion`、`binded_redirect` 视为已连接 | 前者 mock 已录制并断言（注意 SDK 版本号硬编码）；后者见第 3 节"不合并" |
| 2.3.1 | `bot_agent` 字段、`local_token_list`、验证码流程、`binded_redirect`、`notifyStart/notifyStop` | 已合并：`local_token_list`、`binded_redirect`（mock）；未合并：`bot_agent`、验证码交互闭环、`notifyStart/notifyStop`（无 SDK 方法） |
| 2.1.10 / 2.1.9 / 2.1.8 / 2.1.7 / 2.1.4 / 2.1.3 / 2.1.2 | 连接状态通知、出站 hook、Markdown 过滤、插件注册时序、二维码超时调整 | 不合并：均为插件运行时行为；markdown 过滤本仓也没有对应层 |

## 8. 上游行为 与 SDK v0.6.0 的不一致清单

| # | 上游/文档 | SDK v0.6.0 | 本仓影响 / 处理 |
| --- | --- | --- | --- |
| 1 | `get_bot_qrcode` 是 POST + `local_token_list` body | `GET` 无 body（`auth.go` L18-38） | mock 两种都支持；本仓真实 provider 只能走 GET，拿不到 `binded_redirect` |
| 2 | `base_info` 必有 `channel_version` + `bot_agent` | `BaseInfo` 只有 `channel_version`（`types.go` L29-31） | mock 只记录 `channel_version`；`bot_agent` 无法透传 |
| 3 | `WeixinMessage.run_id` | 无该字段（`types.go` L207-222） | mock 从原始 JSON 读 `run_id` 记录 |
| 4 | `MessageItem` type 11/12 与 `tool_call_*_item` | 无定义 | mock 用 `raw_items` 透传；provider 层仍会丢弃 |
| 5 | `need_verifycode` / `verify_code_blocked` / `binded_redirect` | `LoginWithQR` 无分支，落空转直到 8 分钟超时（`auth.go` L126-179） | 本仓 QR 登录在这三种状态下会卡住；mock 已能发出（供未来修复验证） |
| 6 | 轮询可带 `verify_code` | SDK 不发该参数 | 验证码流程 SDK 侧无法完成 |
| 7 | `sendMessage` 非零 `ret` 抛错 | `SendMessage` 不看响应体，只看 HTTP 状态（`client.go` L249-253） | 业务错误静默；mock 对硬错误仍返回 HTTP 500 以让 SDK 报错 |
| 8 | `image_item.aeskey` 优先于 `media.aes_key` | 只用 `media.aes_key`（`cdn.go` L103-119） | mock 同时下发两种，SDK 走 `media.aes_key` 也能解 |
| 9 | `notifyStart` / `notifyStop` 端点 | 无对应方法 | 本仓与 mock 都未实现（不合并） |
| 10 | 插件版本 2.4.10 → `iLink-App-ClientVersion` | 硬编码 `iLinkChannelVersion = "2.1.6"`（`client.go` L27） | 头值偏旧；mock 只断言"正十进制"格式 |
| 11 | 2.4.2 起不再手设 `Content-Length`（Node24 undici 限制） | 仍手设（`client.go` L135） | Go 客户端无该问题，不改 |
| 12 | `getConfig` 仅 `ret==0` 接受配置 | `GetConfig` 不解析 `ret`（`client.go` L278-294） | mock 正常返回 `ret:0`，未额外处理 |
| 13 | `encrypt_type`：0=只加密 fileid，1=打包缩略图/中图 | 常量 `EncryptAES128ECB = 1`（`types.go` L110-113） | 语义命名冲突，未建模 |

## 9. mock server 接口变更一览

新增：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/ilink/bot/get_bot_qrcode?bot_type=3` | 上游表单形式（body 可带 `local_token_list`） |
| POST | `/mock/qr/state` | 强制任意二维码状态：`{"status","redirect_host","verify_code"}` |
| POST | `/mock/qr/bind` | 标记 token 已绑定：`{"bot_token":"..."}` |

扩展：

| 位置 | 变更 |
| --- | --- |
| `GET /ilink/bot/get_qrcode_status` | 新增 `verify_code` 查询参数；响应可带 `redirect_host` |
| `POST /ilink/bot/getupdates` | `get_updates_buf` 推进为服务端新游标；`sync_buf` 兼容保留；可返回 `longpolling_timeout_ms`；`item_list` 支持原样透传 item |
| `POST /ilink/bot/sendmessage` | 记录 `run_id` / `base_info`；`to_user_id` 缺失返回 `ret:-1` |
| `POST /ilink/bot/getuploadurl` | 记录请求；配置 CDN base 后返回 `upload_full_url` |
| `POST /c2c/upload` | 可用 `Engine.SetCDNUploadStatus` 注入失败状态 |
| `POST /mock/inbound` | 新增 `raw_items`、`message_type`；`items[]` 新增 `ref`/`ref_title`/`url`/`playtime`/`sample_rate`/`encode_type`/`bits_per_sample` |
| 引擎可观测 | `Engine.RequestHeaders()`、`LastQRRequest()`、`LastUploadRequest()`、`RawItemsFor()`、`BoundTokens()` |

## 10. 验证

```bash
cd /root/openilink-hub
go test ./internal/provider/ilink/...        # 两个包全绿
go vet ./internal/provider/ilink/...
gofmt -l internal/provider/ilink/*.go internal/provider/ilink/mockserver/*.go
# 仍会列出 clock.go / silk_test.go：它们在本改动前就不符合 gofmt，本次刻意未动
# （仓库共有 41 个这样的文件，只对改动过的文件跑 gofmt -w）。
```

新增测试：`mockserver/protocol_test.go`（15 个协议保真度用例：游标推进、长轮询超时、11/12 透传、`run_id`/`base_info`/`ret`、请求头、二维码 POST/GET、验证码、`scaned_but_redirect`、已绑定、媒体字段、`upload_full_url`、CDN 4xx、mock provider 状态透传）、`mockserver/protocol_e2e_test.go`（真实 provider 走 `full_url` 下载、`ref_msg`/语音转写/密钥编码）、`internal/provider/ilink/bind_test.go`（redirect base 计算）。

## 11. 明确不做的事（附理由）

1. **fork/vendor SDK 或改 `go.mod` 依赖版本**：v0.6.0 是最新发布版；上述 SDK 不一致项应以 issue 形式反馈上游，而不是在本仓打补丁。
2. **`need_verifycode`/`verify_code_blocked` 的 UI 闭环**：需要 `internal/api` + WebUI 增加验证码输入，超出本任务写范围。
3. **`binded_redirect` 作为"登录成功"处理**：需要 API/前端配合，且 `bind.go` 缺可注入 base URL 的测试接缝，不能在不写测试的前提下改登录流程。
4. **tool call item 进入 `provider.MessageItem`**：跨模块（`internal/provider/provider.go`）设计变更。
5. **`provider.Media.URL` 语义拆分**（`image_item.url` vs `media.full_url`）：同上，另需评估存量数据兼容。
6. **notifyStart/notifyStop、缩略图上传、getUpdates 真游标重放**：见第 6/8 节逐条理由。
