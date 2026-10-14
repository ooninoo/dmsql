package main

import (
	"bufio"
	"context"
	"database/sql/driver"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	dm "dm"

	"golang.org/x/term"
)

var commitID = "unknown"

const help = `dmsql — 达梦 SQL 命令行
用法: dmsql [-h host] [-p port] [-U user] [-d schema] [-W] [-c SQL | -f file]
环境变量: DMHOST, DMPORT, DMUSER, DMPASSWORD, DMSCHEMA, DMDSN
交互命令:
  \?              帮助
  \q              退出
  \conninfo       当前连接（隐藏密码）
  \dt [schema]    列出表（默认当前 schema）
  \dn             列出 schema
  \d [schema.]表  查看列定义
  \i 文件         执行 SQL 文件
  \g              执行当前 SQL 缓冲区
  \p / \r         显示 / 清空 SQL 缓冲区
  \x              切换竖排结果
  \timing         切换耗时显示
多行 SQL 以分号结束。-c 将参数作为一条完整 SQL 执行；-f 和标准输入支持多条 SQL。
`

type action struct{ kind, value string }
type actions []action

func (a *actions) add(kind string) func(string) error {
	return func(value string) error { *a = append(*a, action{kind, value}); return nil }
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, in *os.File, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("dmsql", flag.ContinueOnError)
	flags.SetOutput(errOut)
	host := flags.String("h", env("DMHOST", "localhost"), "服务器地址")
	port := flags.String("p", env("DMPORT", "5236"), "端口")
	user := flags.String("U", env("DMUSER", "SYSDBA"), "用户名")
	schema := flags.String("d", env("DMSCHEMA", ""), "schema")
	dsn := flags.String("dsn", "", "完整 dm:// 连接串（默认 DMDSN）")
	passwordPrompt := flags.Bool("W", false, "提示输入密码")
	timeout := flags.Duration("timeout", 0, "每条 SQL 超时（例如 30s，0 为不限）")
	showHelp := flags.Bool("help", false, "帮助")
	showVersion := flags.Bool("version", false, "显示版本")
	var commands actions
	flags.Func("c", "执行一条 SQL（可重复）", commands.add("sql"))
	flags.Func("f", "执行 SQL 文件，- 表示标准输入（可重复）", commands.add("file"))
	flags.Usage = func() { fmt.Fprint(errOut, help); flags.PrintDefaults() }
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *showHelp {
		fmt.Fprint(out, help)
		return 0
	}
	if *showVersion {
		fmt.Fprintln(out, "dmsql commit", commitID)
		return 0
	}
	if flags.NArg() != 0 || *timeout < 0 {
		fmt.Fprintln(errOut, "无效参数；使用 --help 查看用法")
		return 2
	}
	interactive := term.IsTerminal(int(in.Fd())) && len(commands) == 0
	if *dsn == "" {
		*dsn = os.Getenv("DMDSN")
	}
	info := "使用自定义 DSN 连接"
	if *dsn == "" {
		n, err := strconv.Atoi(*port)
		if err != nil || n < 1 || n > 65535 || strings.TrimSpace(*host) == "" || *user == "" {
			fmt.Fprintln(errOut, "主机、用户名或端口无效")
			return 2
		}
		password := os.Getenv("DMPASSWORD")
		if *passwordPrompt || (password == "" && interactive) {
			// Password input uses the terminal so piped SQL is never consumed as a password.
			tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
			if err != nil {
				fmt.Fprintln(errOut, "无法读取终端密码，请设置 DMPASSWORD")
				return 2
			}
			fmt.Fprint(tty, "Password: ")
			bytes, err := term.ReadPassword(int(tty.Fd()))
			fmt.Fprintln(tty)
			tty.Close()
			if err != nil {
				fmt.Fprintln(errOut, "读取密码失败:", err)
				return 2
			}
			password = string(bytes)
		}
		// The bundled SDK parses credentials literally rather than URL-decoding them.
		if strings.ContainsAny(*user, ":?@/") || strings.Contains(password, "?") || strings.ContainsAny(*schema, "?/") {
			fmt.Fprintln(errOut, "当前 SDK 不支持用户名中的 :?@/、密码中的 ? 或 schema 中的 ?/")
			return 2
		}
		*dsn = "dm://" + *user + ":" + password + "@" + net.JoinHostPort(*host, *port)
		if *schema != "" {
			*dsn += "/" + *schema
		}
		*dsn += "?connectTimeout=10000"
		info = fmt.Sprintf("用户 %s，服务器 %s，schema %s", *user, net.JoinHostPort(*host, *port), *schema)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// Restore normal signal handling after the first interrupt; a second
	// interrupt also exits while waiting for terminal input.
	go func() { <-ctx.Done(); stop() }()
	connection, err := dm.OpenConnection(ctx, *dsn)
	if err != nil {
		fmt.Fprintln(errOut, "连接失败:", err)
		return 1
	}
	defer connection.Close()
	s := shell{conn: connection, ctx: ctx, out: out, errOut: errOut, info: info, timeout: *timeout}
	defer func() {
		if s.transaction {
			connection.Rollback()
		}
	}()
	if interactive {
		fmt.Fprintln(out, "dmsql — 输入 \\? 查看帮助，\\q 退出。")
	}
	if len(commands) == 0 {
		err = s.read(in, interactive)
	} else {
		for _, command := range commands {
			if command.kind == "sql" {
				err = s.execute(strings.TrimSpace(command.value))
			} else if command.value == "-" {
				err = s.read(in, false)
			} else {
				err = s.file(command.value)
			}
			if err != nil || s.quit {
				break
			}
		}
	}
	if err != nil {
		fmt.Fprintln(errOut, "错误:", err)
		return 1
	}
	return 0
}

type shell struct {
	conn                   *dm.DmConnection
	ctx                    context.Context
	out, errOut            io.Writer
	info                   string
	timeout                time.Duration
	expanded, timing, quit bool
	transaction            bool
	includeDepth           int
}

func (s *shell) file(path string) error {
	if s.includeDepth >= 16 {
		return errors.New("SQL 文件嵌套超过 16 层")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	s.includeDepth++
	defer func() { s.includeDepth-- }()
	return s.read(file, false)
}

func (s *shell) read(in io.Reader, interactive bool) error {
	reader := bufio.NewReader(in)
	buffer := ""
	for !s.quit {
		if interactive {
			prompt := "dm=> "
			if strings.TrimSpace(buffer) != "" {
				prompt = "dm-> "
			}
			fmt.Fprint(s.out, prompt)
		}
		line, readErr := reader.ReadString('\n')
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		var err error
		trimmed := strings.TrimSpace(line)
		_, _, open := splitSQL(buffer)
		if strings.HasPrefix(trimmed, "\\") && !open {
			err = s.meta(trimmed, &buffer)
		} else {
			buffer += line
			statements, rest, _ := splitSQL(buffer)
			buffer = rest
			for _, statement := range statements {
				if err = s.execute(statement); err != nil {
					if !interactive {
						return err
					}
					fmt.Fprintln(s.errOut, "错误:", err)
					err = nil
				}
			}
		}
		if err != nil {
			if !interactive {
				return err
			}
			fmt.Fprintln(s.errOut, "错误:", err)
		}
		if readErr == io.EOF {
			if s.quit {
				return nil
			}
			_, _, open := splitSQL(buffer)
			if open {
				return errors.New("SQL 中的引号或块注释未闭合")
			}
			return s.execute(buffer)
		}
	}
	return nil
}

func (s *shell) meta(line string, buffer *string) error {
	command, arg, _ := strings.Cut(line, " ")
	// Also accept tabs between command and argument.
	fields := strings.Fields(line)
	if len(fields) > 0 {
		command = fields[0]
		arg = strings.TrimSpace(strings.TrimPrefix(line, command))
	}
	switch command {
	case "\\q":
		s.quit = true
	case "\\?":
		fmt.Fprint(s.out, help)
	case "\\conninfo":
		fmt.Fprintln(s.out, s.info)
	case "\\p":
		fmt.Fprintln(s.out, *buffer)
	case "\\r":
		*buffer = ""
	case "\\g":
		query := *buffer
		*buffer = ""
		return s.execute(query)
	case "\\x":
		s.expanded = !s.expanded
		fmt.Fprintln(s.out, "竖排显示:", s.expanded)
	case "\\timing":
		s.timing = !s.timing
		fmt.Fprintln(s.out, "显示耗时:", s.timing)
	case "\\i":
		if arg == "" {
			return errors.New("用法: \\i 文件")
		}
		return s.file(strings.Trim(arg, "\"'"))
	case "\\dn":
		return s.execute("SELECT USERNAME AS SCHEMA_NAME FROM ALL_USERS ORDER BY USERNAME")
	case "\\dt":
		owner := "SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA')"
		if arg != "" {
			owner = literal(identifier(arg))
		}
		return s.execute("SELECT OWNER, TABLE_NAME FROM ALL_TABLES WHERE OWNER = " + owner + " ORDER BY TABLE_NAME")
	case "\\d":
		if arg == "" {
			return s.meta("\\dt", buffer)
		}
		owner, name := "SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA')", arg
		if before, after, ok := strings.Cut(arg, "."); ok {
			owner, name = literal(identifier(before)), after
		}
		return s.execute("SELECT COLUMN_NAME, DATA_TYPE, DATA_LENGTH, DATA_PRECISION, DATA_SCALE, NULLABLE, DATA_DEFAULT FROM ALL_TAB_COLUMNS WHERE OWNER = " + owner + " AND TABLE_NAME = " + literal(identifier(name)) + " ORDER BY COLUMN_ID")
	default:
		return fmt.Errorf("未知命令 %s；输入 \\? 查看帮助", command)
	}
	return nil
}
func identifier(value string) string {
	if len(value) >= 2 && strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		return strings.ReplaceAll(value[1:len(value)-1], "\"\"", "\"")
	}
	return strings.ToUpper(value)
}
func literal(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

func (s *shell) execute(query string) error {
	query = sqlBody(query)
	if query == "" {
		return nil
	}
	ctx := s.ctx
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return err
	}
	command := strings.ToUpper(strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(query), ";")), " "))
	switch command {
	case "BEGIN", "BEGIN TRANSACTION", "START TRANSACTION":
		if s.transaction {
			return errors.New("事务已经开始")
		}
		if _, err := s.conn.BeginTx(ctx, driver.TxOptions{}); err != nil {
			return err
		}
		s.transaction = true
		fmt.Fprintln(s.out, "BEGIN")
		return nil
	case "COMMIT", "COMMIT WORK", "ROLLBACK", "ROLLBACK WORK":
		if !s.transaction {
			return errors.New("没有活动事务")
		}
		var err error
		if strings.HasPrefix(command, "COMMIT") {
			err = s.conn.Commit()
		} else {
			err = s.conn.Rollback()
		}
		if err != nil {
			return err
		}
		s.transaction = false
		fmt.Fprintln(s.out, command)
		return nil
	}
	result, err := s.conn.ExecuteContext(ctx, query)
	if err != nil {
		return err
	}
	switch value := result.(type) {
	case *dm.DmRows:
		err = s.printRows(value)
		closeErr := value.Close()
		if err == nil {
			err = closeErr
		}
	case *dm.DmResult:
		count, resultErr := value.RowsAffected()
		if resultErr != nil {
			return resultErr
		}
		if count >= 0 {
			fmt.Fprintf(s.out, "OK (%d rows affected)\n", count)
		} else {
			fmt.Fprintln(s.out, "OK")
		}
	default:
		return fmt.Errorf("未知执行结果 %T", result)
	}
	if s.timing {
		fmt.Fprintf(s.out, "Time: %.3f ms\n", float64(time.Since(start).Microseconds())/1000)
	}
	return err
}

func (s *shell) printRows(rows driver.Rows) error {
	for {
		columns := rows.Columns()
		// ponytail: aligned tables buffer one result set; use \x for large results.
		writer := tabwriter.NewWriter(s.out, 0, 4, 2, ' ', 0)
		if !s.expanded {
			fmt.Fprintln(writer, strings.Join(columns, "\t"))
		}
		values := make([]driver.Value, len(columns))
		count := 0
		for {
			err := rows.Next(values)
			if err == io.EOF {
				break
			}
			if err != nil {
				writer.Flush()
				return err
			}
			count++
			if s.expanded {
				fmt.Fprintf(writer, "-[ RECORD %d ]-\n", count)
			}
			cells := make([]string, len(values))
			for i, value := range values {
				cells[i] = formatValue(value)
				if s.expanded {
					fmt.Fprintf(writer, "%s\t| %s\n", columns[i], cells[i])
				}
			}
			if !s.expanded {
				fmt.Fprintln(writer, strings.Join(cells, "\t"))
			} else if err := writer.Flush(); err != nil {
				return err
			}
		}
		if err := writer.Flush(); err != nil {
			return err
		}
		fmt.Fprintf(s.out, "(%d rows)\n", count)
		next, ok := rows.(driver.RowsNextResultSet)
		if !ok || !next.HasNextResultSet() {
			return nil
		}
		if err := next.NextResultSet(); err != nil {
			return err
		}
	}
}
func formatValue(value interface{}) string {
	var text string
	switch v := value.(type) {
	case nil:
		return "NULL"
	case []byte:
		text = string(v)
	case time.Time:
		text = v.Format("2006-01-02 15:04:05.999999999Z07:00")
	default:
		text = fmt.Sprint(v)
	}
	return strings.NewReplacer("\t", "\\t", "\r", "\\r", "\n", "\\n").Replace(text)
}

// splitSQL recognizes SQL quotes and comments, so semicolons inside them do not
// submit a statement. Procedural blocks can be sent intact with -c.
func splitSQL(text string) (statements []string, rest string, open bool) {
	start, quote, depth := 0, byte(0), 0
	lineComment := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		next := byte(0)
		if i+1 < len(text) {
			next = text[i+1]
		}
		if lineComment {
			if ch == '\n' {
				lineComment = false
			}
			continue
		}
		if depth > 0 {
			if ch == '/' && next == '*' {
				depth++
				i++
			} else if ch == '*' && next == '/' {
				depth--
				i++
			}
			continue
		}
		if quote != 0 {
			if ch == quote {
				if next == quote {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		switch {
		case ch == '-' && next == '-':
			lineComment = true
			i++
		case ch == '/' && next == '*':
			depth = 1
			i++
		case ch == '\'' || ch == '"':
			quote = ch
		case ch == ';':
			statements = append(statements, text[start:i])
			start = i + 1
		}
	}
	return statements, text[start:], quote != 0 || depth > 0
}
func sqlBody(text string) string {
	for i := 0; i < len(text); {
		switch {
		case strings.ContainsRune(" \t\r\n;", rune(text[i])):
			i++
		case strings.HasPrefix(text[i:], "--"):
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				return ""
			}
			i += end + 1
		case strings.HasPrefix(text[i:], "/*"):
			depth := 1
			i += 2
			for i < len(text) && depth > 0 {
				if strings.HasPrefix(text[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(text[i:], "*/") {
					depth--
					i += 2
				} else {
					i++
				}
			}
		default:
			return text[i:]
		}
	}
	return ""
}
