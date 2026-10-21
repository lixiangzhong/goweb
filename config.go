package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

// configFileName 是项目根下的配置文件。
const configFileName = ".goweb.yml"

// fileConfig 是 .goweb.yml 的结构；字段用指针以区分「未配置」与「配置成零值」。
// 键名与命令行 flag 同名，避免两套词汇。
type fileConfig struct {
	Dir         *string  `yaml:"dir"`
	Template    *string  `yaml:"template"`
	Assets      *string  `yaml:"assets"`
	Out         *string  `yaml:"out"`
	Mode        *string  `yaml:"mode"`
	BuildTime   *string  `yaml:"build-time"`
	Fingerprint *bool    `yaml:"fingerprint"`
	LiveReload  *bool    `yaml:"livereload"`
	Addr        *string  `yaml:"addr"`
	Open        *bool    `yaml:"open"`
	Minify      *bool    `yaml:"minify"`
	Gitignore   *bool    `yaml:"gitignore"`
	Ignore      []string `yaml:"ignore"`
}

// loadFileConfig 加载配置文件并给出项目根。
//
//   - explicit 非空：读取该路径（相对运行目录），项目根 = 该文件所在目录；
//   - explicit 为空：从 start 逐级向上查找 .goweb.yml，找到即用它，项目根 = 它的目录；
//     找不到则没有配置，项目根 = start。
func loadFileConfig(explicit, start string) (fileConfig, string, error) {
	startAbs, err := filepath.Abs(start)
	if err != nil {
		return fileConfig{}, "", fmt.Errorf("解析运行目录 %q: %w", start, err)
	}

	if explicit != "" {
		configPath := explicit
		if !filepath.IsAbs(configPath) {
			configPath = filepath.Join(startAbs, configPath)
		}
		configPath = filepath.Clean(configPath)

		config, err := parseFileConfig(configPath)
		if err != nil {
			return fileConfig{}, "", err
		}
		return config, filepath.Dir(configPath), nil
	}

	for dir := startAbs; ; {
		candidate := filepath.Join(dir, configFileName)
		if _, err := os.Stat(candidate); err == nil {
			config, err := parseFileConfig(candidate)
			if err != nil {
				return fileConfig{}, "", err
			}
			return config, dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return fileConfig{}, startAbs, nil
}

// parseFileConfig 解析配置文件；未知字段直接报错，避免拼错字段名后静默无效。
func parseFileConfig(path string) (fileConfig, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fileConfig{}, fmt.Errorf("配置文件不存在：%s", path)
		}
		return fileConfig{}, fmt.Errorf("读取配置文件 %s: %w", path, err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)

	var config fileConfig
	if err := decoder.Decode(&config); err != nil {
		if errors.Is(err, io.EOF) {
			// 空文件等价于没有配置。
			return fileConfig{}, nil
		}
		return fileConfig{}, fmt.Errorf("解析配置文件 %s: %s", path, friendlyConfigError(err))
	}
	return config, nil
}

// unknownFieldPattern 匹配 yaml.v3 在 KnownFields(true) 下的未知字段报错，
// 例如 `line 1: field dirr not found in type main.fileConfig`。
var unknownFieldPattern = regexp.MustCompile(`field (\S+) not found in type \S+`)

// friendlyConfigError 把 yaml 的原始报错整理成对用户更有用的信息：
// 未知字段（拼错字段名是最常见的错误）会附上可用字段清单，而不是暴露 Go 类型名。
func friendlyConfigError(err error) string {
	if matches := unknownFieldPattern.FindStringSubmatch(err.Error()); matches != nil {
		return fmt.Sprintf("未知配置字段 %q；可用字段：%s", matches[1], strings.Join(configKeys(), "、"))
	}
	return err.Error()
}

// configKeys 返回配置文件支持的键（从结构体标签读取，避免两处维护）。
func configKeys() []string {
	structType := reflect.TypeOf(fileConfig{})
	keys := make([]string, 0, structType.NumField())
	for i := 0; i < structType.NumField(); i++ {
		if key := structType.Field(i).Tag.Get("yaml"); key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// pickString 按 flag > 配置 > 默认 取字符串值。
func pickString(flagValue string, flagSet bool, configValue *string, fallback string) string {
	if flagSet {
		return flagValue
	}
	if configValue != nil {
		return *configValue
	}
	return fallback
}

// pickBool 按 flag > 配置 > 默认 取布尔值。
func pickBool(flagValue bool, flagSet bool, configValue *bool, fallback bool) bool {
	if flagSet {
		return flagValue
	}
	if configValue != nil {
		return *configValue
	}
	return fallback
}

// settings 是解析后的运行设置：flag > 配置文件 > 默认值。
type settings struct {
	projectRoot  string
	pagesRoot    string
	templateRoot string
	assetsRoot   string
	out          string
	mode         Mode
	buildTime    time.Time
	fingerprint  bool
	liveReload   bool
	open         bool
	minify       bool
	addr         string
	// addrExplicit 表示 addr 来自 --addr 或配置（而不是内置默认值）：
	// 只有非显式指定时才允许端口被占用后自动递增。
	addrExplicit bool
	ignore       []string
	useGitignore bool
}

// localFlags 是各子命令自己的参数（不共享，避免被另一个子命令静默接受）。
type localFlags struct {
	addr       string
	liveReload bool
	open       bool
	out        string
	minify     bool
}

// resolveSettings 合并命令行 flag 与 .goweb.yml，得到最终设置。
// devCommand 为真表示当前是 serve（--mode auto 时取 dev）。
func resolveSettings(cmd *cobra.Command, globals *globalOptions, local localFlags, devCommand bool) (settings, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return settings{}, fmt.Errorf("获取运行目录: %w", err)
	}

	config, projectRoot, err := loadFileConfig(globals.config, workingDir)
	if err != nil {
		return settings{}, err
	}
	changed := func(name string) bool { return cmd.Flags().Changed(name) }

	resolved := settings{
		projectRoot:  projectRoot,
		pagesRoot:    pickString(globals.dir, changed("dir"), config.Dir, defaultPageDir),
		templateRoot: pickString(globals.templateDir, changed("template"), config.Template, defaultTemplateDir),
		assetsRoot:   pickString(globals.assetsRoot, changed("assets"), config.Assets, defaultAssetsRoot),
		out:          pickString(local.out, changed("out"), config.Out, defaultOutDir),
		minify:       pickBool(local.minify, changed("minify"), config.Minify, true),
		fingerprint:  pickBool(globals.fingerprint, changed("fingerprint"), config.Fingerprint, true),
		liveReload:   pickBool(local.liveReload, changed("livereload"), config.LiveReload, true),
		open:         pickBool(local.open, changed("open"), config.Open, true),
		addr:         pickString(local.addr, changed("addr"), config.Addr, defaultAddr),
		useGitignore: pickBool(false, false, config.Gitignore, true),
		ignore:       config.Ignore,
	}

	// 「显式指定端口」= 命令行给了 --addr，或配置文件里写了 addr。
	resolved.addrExplicit = changed("addr") || config.Addr != nil

	rawMode := pickString(globals.mode, changed("mode"), config.Mode, modeAuto)
	if resolved.mode, err = resolveMode(rawMode, devCommand); err != nil {
		return settings{}, err
	}

	rawBuildTime := pickString(globals.buildTime, changed("build-time"), config.BuildTime, "")
	if resolved.buildTime, err = parseBuildTime(rawBuildTime); err != nil {
		return settings{}, err
	}

	return resolved, nil
}

// siteOptions 把解析后的设置转成 SiteOptions。
func (s settings) siteOptions(excludeDirs []string, publishing bool) SiteOptions {
	return SiteOptions{
		ProjectRoot:    s.projectRoot,
		Dir:            s.pagesRoot,
		TemplateDir:    s.templateRoot,
		AssetsRoot:     s.assetsRoot,
		ExcludeDirs:    excludeDirs,
		IgnorePatterns: s.ignore,
		UseGitignore:   s.useGitignore,
		Publishing:     publishing,
		Mode:           s.mode,
		BuildTime:      s.buildTime,
		Fingerprint:    s.fingerprint,
	}
}

// resolveOutput 把产物目录解析成绝对路径（相对路径相对项目根）。
func (s settings) resolveOutput() (string, error) {
	out := strings.TrimSpace(s.out)
	if out == "" {
		out = defaultOutDir
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(s.projectRoot, out)
	}
	return filepath.Abs(out)
}
