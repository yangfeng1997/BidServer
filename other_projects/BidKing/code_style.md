# Go 代码命名规范（Google Style）

本文档基于 [Google Go Style Guide](https://google.github.io/styleguide/go/) 和官方 [Effective Go](https://go.dev/doc/effective_go) 实践整理。

---

## 目录

1. [通用原则](#1-通用原则)
2. [包名](#2-包名)
3. [文件名](#3-文件名)
4. [变量名](#4-变量名)
5. [常量名](#5-常量名)
6. [函数与方法名](#6-函数与方法名)
7. [接口名](#7-接口名)
8. [结构体与类型名](#8-结构体与类型名)
9. [接收者名](#9-接收者名)
10. [错误变量名](#10-错误变量名)
11. [缩写与首字母缩略词](#11-缩写与首字母缩略词)
12. [测试命名](#12-测试命名)
13. [禁止事项速查](#13-禁止事项速查)

---

## 1. 通用原则

- **可读性优先**：名字的首要目的是让读者理解代码，而不是节省击键次数。
- **就近原则**：名字的使用位置离声明越近，可以越短；距离越远，需要越具描述性。
- **避免冗余**：名字不应重复其所在包或类型已经表达的信息。
- **避免歧义**：同一作用域内不能出现仅靠大小写区分的名字（如 `count` 和 `Count`）。
- **不使用下划线**：除测试辅助函数、`_test.go` 文件内的 `TestXxx_subcase` 以及生成代码外，**不使用下划线分隔单词**。

---

## 2. 包名

### 规则

- 全部**小写字母**，**不使用下划线**，**不使用驼峰**。
- 尽量使用**单个单词**；确实需要多词时直接拼接（`httputil`，而非 `http_util` 或 `httpUtil`）。
- 包名应是**名词**，描述包提供的能力，而非包含的内容。
- 避免使用过于宽泛的名字：`util`、`common`、`misc`、`helper` 单独作为包名时意义模糊——如果必须用，在前面加限定词（`mathutil`、`syncutil`）。
- 包名不必与目录名完全一致，但强烈建议保持一致以降低认知负担。

### 示例

```go
// 好
package acceptor
package serialize
package cluster

// 差——使用了下划线或驼峰
package tcp_acceptor
package tcpAcceptor

// 差——过于宽泛
package util
package common
```

### 包路径与包名

导入路径的最后一段即为包名，调用方通过包名访问导出标识符，因此**包名会成为标识符前缀的一部分**。

```go
// 包名 json，导出类型 Serializer
// 调用方写：json.NewSerializer()   ← 自然流畅
// 不要写：json.JSONSerializer{}    ← "JSON" 已由包名表达，重复
```

---

## 3. 文件名

- 全部**小写字母**，多词用**下划线**分隔（这是文件系统惯例，与包内标识符命名规则不同）。
- 测试文件以 `_test.go` 结尾。
- 平台相关文件使用 `_linux.go`、`_windows.go` 等后缀（Go 工具链自动识别）。

```
acceptor.go
tcp_acceptor.go
tcp_acceptor_test.go
zap_adapter.go
example_test.go
```

---

## 4. 变量名

### 局部变量

- 使用**驼峰命名**（camelCase）。
- **作用域越小，名字越短**：循环变量用单字母（`i`、`j`、`k`），临时变量用缩写（`buf`、`cfg`、`err`）。
- **作用域越大，名字越描述性**：包级变量、跨函数传递的变量使用完整单词。

```go
// 循环变量——单字母完全合适
for i, v := range items { ... }

// 短作用域——缩写可读
buf := make([]byte, 1024)
cfg := loadConfig()

// 长作用域——完整单词
var globalTimeout = 30 * time.Second
```

### 布尔变量

- 用肯定形式命名，读起来像自然语言中的断言。
- 常用前缀：`is`、`has`、`ok`、`can`、`should`、`enabled`。

```go
// 好
isRunning := true
hasPermission := checkPerm(uid)
ok := cache.Get(key)

// 差——否定形式增加认知负担
isNotReady := false
```

### 包级变量

- 导出变量使用 **PascalCase**。
- 非导出变量使用 **camelCase**，不加 `g`、`_` 等前缀。

```go
// 好
var defaultTimeout = 5 * time.Second   // 非导出
var MaxRetries = 3                      // 导出

// 差
var g_timeout = 5 * time.Second        // 匈牙利命名，Go 不用
var GlobalTimeout = 5 * time.Second    // "Global" 前缀冗余
```

---

## 5. 常量名

- 使用 **PascalCase**（导出）或 **camelCase**（非导出）。
- **不使用** `ALL_CAPS_WITH_UNDERSCORES`——这是 C/Java 风格，在 Go 中不规范。
- `iota` 枚举的第一个值通常命名为 `TypeUnknown` 或 `LevelInvalid`，明确表示零值语义。

```go
// 好
const (
    DebugLevel Level = iota
    InfoLevel
    WarnLevel
    ErrorLevel
    FatalLevel
)

const MaxPacketSize = 65535

// 差——ALL_CAPS 风格
const MAX_PACKET_SIZE = 65535
const DEBUG_LEVEL = 0
```

---

## 6. 函数与方法名

### 通用规则

- 导出函数使用 **PascalCase**，非导出函数使用 **camelCase**。
- 名字应描述**函数做什么**（动词或动词短语），而非怎么做。
- 返回值显而易见时，可省略返回类型描述（`NewSerializer` 而非 `NewSerializerInstance`）。

### 构造函数

- 返回单一类型：`New` + 类型名，如 `NewAcceptor()`、`NewZapBackend()`。
- 同一包内有多种构造方式：加修饰词，如 `NewZapDevelopment()`、`NewZapProduction()`、`NewZapFileLogger()`。

```go
// 好
func NewSerializer() *Serializer
func NewZapDevelopment() (Logger, error)
func NewZapProductionWithConfig(cfg zap.Config) (Logger, error)

// 差——"Create" 和 "Make" 不是 Go 惯例
func CreateSerializer() *Serializer
func MakeLogger() Logger
```

### Getter / Setter

Go 不强制使用 `GetXxx`/`SetXxx`。
- Getter：直接用字段名（**不加 `Get` 前缀**）。
- Setter：用 `SetXxx`。

```go
// 好
func (s *Server) ID() string          // getter，不写 GetID
func (s *Server) SetID(id string)     // setter

// 差
func (s *Server) GetID() string
```

### 返回 bool 的函数

用 `Is`、`Has`、`Can`、`Should` 等前缀，读起来像问句。

```go
func (l *coreLogger) IsEnabled(level Level) bool
func (app *Application) IsRunning() bool
func (app *Application) IsFrontend() bool
```

---

## 7. 接口名

- **单方法接口**：方法名加 `-er` 后缀，如 `Reader`、`Writer`、`Marshaler`、`Unmarshaler`、`Closer`。
- **多方法接口**：使用描述能力的名词，如 `Serializer`、`Logger`、`Backend`。

```go
// 标准库风格（单方法）
type Marshaler interface {
    Marshal(any) ([]byte, error)
}
```

---

## 8. 结构体与类型名

- 使用 **PascalCase**（导出）或 **camelCase**（非导出）。
- 名字应是**名词**，描述它代表的事物。
- 不加 `Struct`、`Class`、`Object` 等后缀。
- 内部实现结构体（不导出）可使用描述性的小写名字，如 `coreLogger`、`rotatingWriter`、`zapSugared`。

```go
// 好
type Server struct { ... }          // 导出
type FileLoggerConfig struct { ... } // 导出，Config 后缀表明用途
type coreLogger struct { ... }      // 非导出实现细节
type rotatingWriter struct { ... }  // 非导出实现细节

// 差
type ServerStruct struct { ... }    // 冗余后缀
type LoggerObject struct { ... }    // 冗余后缀
```

### 类型别名与自定义类型

```go
// 好——类型名清晰表达语义
type Level int8
type Format string
type FieldType uint8
type OnConnectFunc func(conn net.Conn)

// 差——丢失语义
type MyInt int8
type Callback func(conn net.Conn)
```

---

## 9. 接收者名

- 使用**类型名的首字母缩写**（1~2 个字母），**尽量不使用** `self`、`this`、`me`。
- 同一类型的所有方法必须使用**相同的接收者名**。
- 接收者名应尽量短：`l` for `Logger`，`b` for `Backend`，`w` for `Writer`。
- 单字母是否足够，取决于同一文件内类型缩写是否唯一：同文件只有一个类型时单字母即可；有多个类型时，适当用多个缩写字母加以区分（如 `cl` for `coreLogger`，`zb` for `ZapBackend`），避免混淆。

```go
// 好
func (l *coreLogger) Info(msg string, fields ...Field) { ... }
func (l *coreLogger) With(fields ...Field) Logger { ... }

func (b *ZapBackend) Log(level Level, msg string, fields []Field) { ... }
func (b *ZapBackend) With(fields []Field) Backend { ... }

func (w *rotatingWriter) Write(p []byte) (int, error) { ... }
func (w *rotatingWriter) Close() error { ... }

// 差
func (this *coreLogger) Info(...) { ... }   // this 是 Java/C++ 风格
func (self *ZapBackend) Log(...) { ... }    // self 是 Python 风格
func (logger *coreLogger) Info(...) { ... } // 过长
```

---

## 10. 错误变量名

### 哨兵错误（包级错误变量）

- 导出哨兵错误：`Err` + 描述，如 `ErrWrongValueType`、`ErrTimeout`。
- 非导出哨兵错误：`err` + 描述（camelCase），如 `errInvalidState`。

```go
// 好
var ErrWrongValueType = errors.New("protobuf: convert on wrong type value")
var errConnectionClosed = errors.New("connection already closed")

// 差
var WrongTypeError = errors.New(...)   // 前缀应为 Err
var ERROR_WRONG_TYPE = errors.New(...) // ALL_CAPS 风格
```

### 局部错误变量

- 通常直接用 `err`。
- 同作用域需要区分多个错误时，加上下文前缀：`parseErr`、`writeErr`。

```go
result, err := doSomething()
if err != nil { ... }

openErr := file.Open(name)
writeErr := file.Write(data)
```

### 自定义错误类型

- 类型名以 `Error` 结尾：`ValidationError`、`TimeoutError`。
- 必须实现 `error` 接口（`Error() string` 方法）。

```go
type ValidationError struct {
    Field   string
    Message string
}

func (e *ValidationError) Error() string {
    return fmt.Sprintf("validation failed on %s: %s", e.Field, e.Message)
}
```

---

## 11. 缩写与首字母缩略词

Google 规范：**缩略词整体大写或整体小写**，不拆分处理。

| 缩略词 | 导出（PascalCase 上下文） | 非导出（camelCase 上下文） |
|--------|--------------------------|---------------------------|
| URL    | `RequestURL`、`ParseURL` | `requestURL`、`parseURL`  |
| HTTP   | `HTTPClient`、`NewHTTP`  | `httpClient`              |
| ID     | `UserID`、`RoomID`       | `userID`、`roomID`        |
| IP     | `ClientIP`               | `clientIP`                |
| JSON   | `MarshalJSON`            | `marshalJSON`             |
| TCP    | `TCPAcceptor`            | `tcpAcceptor`             |
| TLS    | `TLSConfig`              | `tlsConfig`               |
| RPC    | `RPCHandler`             | `rpcHandler`              |
| DB     | `DBPool`                 | `dbPool`                  |

```go
// 好
type TCPAcceptor struct { ... }
func ParseURL(raw string) (*url.URL, error) { ... }
userID := "u_abc"

// 差——拆分缩略词
type TcpAcceptor struct { ... }
func ParseUrl(raw string) (*url.URL, error) { ... }
userId := "u_abc"
```

---

## 12. 测试命名

### 测试函数

```
Test<被测函数或方法>[_<子场景>](t *testing.T)
```

- 主函数测试：`TestMarshal`、`TestNewAcceptor`
- 子场景用下划线分隔（**测试函数内部是唯一允许使用下划线的地方**）：
  `TestMarshal_EmptyInput`、`TestMarshal_NilProtoMessage`

### 表驱动测试中的用例名

- 用简短的英文短语或中文描述，不必以 `Test` 开头。

```go
tests := []struct {
    name  string
    input string
    want  int
}{
    {name: "empty string", input: "", want: 0},
    {name: "single char", input: "a", want: 1},
}
```

### 基准测试

```
Benchmark<被测函数>[_<子场景>](b *testing.B)
```

```go
func BenchmarkMarshal(b *testing.B) { ... }
func BenchmarkMarshal_LargePayload(b *testing.B) { ... }
```

### Example 函数

```
Example<类型或函数>[_<方法>][_<后缀>]()
```

```go
func Example_init() { ... }          // 包级示例
func ExampleNewSerializer() { ... }  // 函数示例
func ExampleLogger_With() { ... }    // 方法示例
```

---

## 13. 禁止事项速查

| 禁止 | 原因 | 正确做法 |
|------|------|----------|
| `getUserId()` | 缩略词 `ID` 应整体大写 | `getUserID()` |
| `parseUrl()` | 缩略词 `URL` 应整体大写 | `parseURL()` |
| `MAX_SIZE = 100` | Go 不用 ALL_CAPS 常量 | `MaxSize = 100` |
| `func (this *T) Foo()` | `this` 是 Java 风格 | `func (t *T) Foo()` |
| `func GetName() string` | Getter 不加 Get 前缀 | `func Name() string` |
| `func CreateFoo() *Foo` | `Create` 不是 Go 构造惯例 | `func NewFoo() *Foo` |
| `type FooStruct struct{}` | 后缀 `Struct` 冗余 | `type Foo struct{}` |
| `package httpUtil` | 包名不用驼峰 | `package httputil` |
| `var g_timeout = ...` | 匈牙利命名，Go 不用 | `var defaultTimeout = ...` |
| `var WrongType = errors.New(...)` | 哨兵错误应以 `Err` 开头 | `var ErrWrongType = errors.New(...)` |
| `type TcpAcceptor struct{}` | 缩略词应整体大写 | `type TCPAcceptor struct{}` |
