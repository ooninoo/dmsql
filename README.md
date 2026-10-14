# dmsql

基于本地 `pkg/dmsdk` 的达梦数据库命令行，提供类似 PostgreSQL `psql` 的 SQL 交互方式。

```sh
go build -buildvcs=false -o dmsql ./cmd
./dmsql -h 127.0.0.1 -p 5236 -U SYSDBA -W
```

`-W` 从终端读取密码，不回显；也可设置 `DMPASSWORD`。`-d` 指定 schema，不是 PostgreSQL 数据库名。默认连接 `localhost:5236`，用户 `SYSDBA`。

```sh
# 执行查询、修改或 DDL；-c 可重复，按参数顺序执行
./dmsql -c 'SELECT * FROM DUAL'
./dmsql -c 'CREATE TABLE DEMO (ID INT, NAME VARCHAR(100))'
./dmsql -c "INSERT INTO DEMO VALUES (1, '达梦')" -c 'SELECT * FROM DEMO'

# 执行文件或管道 SQL，失败即停止并返回非零状态
./dmsql -f init.sql
printf 'SELECT * FROM DUAL;\n' | ./dmsql
./dmsql -f - < init.sql

# 自定义 SDK 连接参数（连接串中含密码，建议通过 DMDSN 环境变量传入）
./dmsql --dsn 'dm://SYSDBA:password@127.0.0.1:5236?connectTimeout=5000'
./dmsql --timeout 30s -c 'SELECT * FROM DUAL'
./dmsql --help
```

支持环境变量 `DMHOST`、`DMPORT`、`DMUSER`、`DMPASSWORD`、`DMSCHEMA`、`DMDSN`。设置 DSN 后连接参数由 DSN 决定，`-W` 不生效。当前 SDK 按原文解析凭据，不做 URL 解码；普通连接参数会校验 SDK 不支持的分隔字符。

交互示例：

```sql
\conninfo
\dn
\dt
\dt SYSDBA
\d SYSDBA.DEMO
SELECT ID, NAME
FROM DEMO
WHERE ID = 1;
\x
\timing
\i init.sql
\q
```

| 命令 | 功能 |
| --- | --- |
| `\?` | 帮助 |
| `\q` | 退出 |
| `\conninfo` | 连接信息，隐藏密码 |
| `\dn` | 查看可见 schema |
| `\dt [schema]` | 查看表，默认当前 schema |
| `\d [schema.]table` | 查看列类型、长度、精度、可空性、默认值 |
| `\i path` | 执行文件（路径相对当前工作目录） |
| `\g` | 执行当前 SQL 缓冲区，无需分号 |
| `\p` / `\r` | 显示 / 清空 SQL 缓冲区 |
| `\x` | 切换竖排输出 |
| `\timing` | 切换耗时输出 |

SQL 支持多行、同一行多条语句、字符串及双引号标识符中的分号、行注释和块注释；文件或标准输入最后一条 SQL 可不带分号。未闭合的引号、块注释会报错。交互中 SQL 出错后可继续输入。

同一次运行复用一个连接，`SET SCHEMA ...` 的结果会保留。`BEGIN` / `BEGIN TRANSACTION` / `START TRANSACTION`、`COMMIT`、`ROLLBACK` 调用 SDK 事务方法，普通 SQL 默认自动提交，退出时回滚未结束的显式事务。达梦 DDL 的隐式提交行为由数据库决定。

```sql
BEGIN;
INSERT INTO DEMO VALUES (2, '事务示例');
ROLLBACK;
```

`-c` 将 SQL 原样作为一条完整语句提交，也可用于包含内部分号的达梦过程块；文件和交互模式按分号拆分，不解析过程块语法。SQL 语法使用达梦语法。当前版本不提供历史记录、自动补全或分页器；表格输出会缓冲当前结果集，较大查询建议使用 `\x` 竖排输出。

退出码：`0` 成功；`1` 连接、SQL 或文件错误；`2` 参数错误。Ctrl+C 取消当前操作，可能导致 SDK 关闭连接，此时应退出并重新连接；等待输入时可用 `\q` 或再次 Ctrl+C 退出。

验证：

```sh
go test ./...
go vet ./...
```

## Makefile 构建

```sh
make                 # 生成 bin/dmsql
make test
make vet
make clean           # 删除构建的二进制
make build BINARY=./dmsql
GOOS=linux GOARCH=arm64 make build BINARY=bin/dmsql-linux-arm64
```

本地和镜像构建都使用 `CGO_ENABLED=0`，生成静态二进制；SDK 的第三方 Go 加密插件需要 CGO，不包含在这一构建方式中。

## Docker 构建与运行

```sh
make docker-build                         # 镜像 dmsql:latest
make docker-build IMAGE=dmsql:v1
# 或直接使用 Dockerfile
docker build -t dmsql:latest .
docker run --rm dmsql:latest --help

# 交互连接，终端输入密码
docker run --rm -it dmsql:latest -h 192.168.1.10 -p 5236 -U SYSDBA -W

# 使用宿主机已有的 DMPASSWORD 环境变量执行 SQL
docker run --rm -e DMPASSWORD dmsql:latest -h 192.168.1.10 -c 'SELECT * FROM DUAL'

# 挂载 SQL 文件或通过标准输入执行
docker run --rm -e DMPASSWORD -v "$PWD:/work:ro" dmsql:latest -h 192.168.1.10 -f /work/init.sql
printf 'SELECT * FROM DUAL;\n' | docker run --rm -i -e DMPASSWORD dmsql:latest -h 192.168.1.10
```

镜像采用多阶段构建，运行阶段仅包含程序和 CA 证书，以非 root 用户运行。文件挂载需允许该用户读取。容器中的 `localhost` 指容器自身；连接宿主机上的达梦服务，在 Linux 上可使用 `--network host`，或传入容器可访问的数据库地址。交互使用 `-it`，管道输入使用 `-i`。
