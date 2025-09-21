package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	Src               string            `json:"src"`
	EntryPoint        string            `json:"entryPoint"`
	Root              string            `json:"root"`
	IndexCandidates   []string          `json:"indexCandidates"`
	IgnoredPatterns   []string          `json:"ignoredPatterns"`
	Aliases           map[string]string `json:"aliases"`
	AllowedExtensions map[string]bool   `json:"allowedExtensions"`
}

var cfg *Config

func main() {
	if err := loadConfig("config.json"); err != nil {
		panic(err)
	}

	if info, err := os.Stat(cfg.Src); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "Invalid source directory: %v\n", err)
		os.Exit(1)
	}

	if _, err := os.Stat(cfg.EntryPoint); err != nil {
		fmt.Fprintf(os.Stderr, "Entry point not found: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("🔍 Scanning files in %s\n", cfg.Src)

	files, err := findFiles()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning files: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("📊 Total processed files: %d\n", len(files))

	graph := &ImportGraph{
		Used:    make(map[string]bool),
		Visited: make(map[string]bool),
	}

	fmt.Printf("🔗 Starting from entry point: %s\n", cfg.EntryPoint)
	graph.Traverse(cfg.EntryPoint)

	findUnusedAndDuplicatedFiles(graph, files)
}

func loadConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return err
	}
	cfg = &c
	return nil
}

func findUnusedAndDuplicatedFiles(graph *ImportGraph, files []string) {
	var unused []string
	hashedFiles := make(map[string][]string)
	duplicated := make(map[string][]string)

	fmt.Printf("📁 Checking all files...\n")

	for _, file := range files {
		aliasPath := strings.TrimPrefix(file, cfg.Src)
		fileHash, err := hashFileMD5(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Cannot read %s: %v\n", file, err)
		} else {
			hashedFiles[fileHash] = append(hashedFiles[fileHash], aliasPath)
		}
		if len(hashedFiles[fileHash]) > 1 {
			duplicated[fileHash] = hashedFiles[fileHash]
		}
		used := graph.Used[file]
		if !used {
			unused = append(unused, aliasPath)
		}
	}

	writeToJsonFile("unused", unused)
	writeToJsonFile("dublicates", duplicated)

	processUnused(unused)
	processDublicated(duplicated)

}

func processDublicated(duplicated map[string][]string) {
	duplicatedTotal := len(duplicated)

	if duplicatedTotal == 0 {
		fmt.Println("✅ No duplicates found!")
	} else {
		fmt.Printf("⚠️  Duplicated files: %d\n", duplicatedTotal)
	}
}

func processUnused(unused []string) {
	unusedTotal := len(unused)

	if unusedTotal == 0 {
		fmt.Println("\n✅ All files are used!")
	} else {
		fmt.Printf("\n❌ Unused files: %d\n", unusedTotal)
	}
}

func writeToJsonFile(fileName string, data any) {
	jsonData, _ := json.MarshalIndent(data, "", "    ")
	err := os.WriteFile(fileName+".json", jsonData, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️ Failed to save %s.json: %v\n", fileName, err)
	}
}

func hashFileMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func findFiles() ([]string, error) {
	var files []string

	err := filepath.WalkDir(cfg.Src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}

		name := d.Name()

		for _, pattern := range cfg.IgnoredPatterns {
			if strings.Contains(name, pattern) {
				return nil
			}
		}

		if strings.HasSuffix(name, ".module.scss") {
			files = append(files, filepath.Clean(path))
			return nil
		}

		if cfg.AllowedExtensions[filepath.Ext(name)] {
			files = append(files, filepath.Clean(path))
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return files, nil
}

func extractImports(filePath string) ([]string, error) {
	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		return nil, err
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var imports []string

	// from './apps/...'
	fromMatches := regexp.MustCompile(`from\s+['"]([^'"]+)['"]`).FindAllStringSubmatch(string(content), -1)
	for _, match := range fromMatches {
		imports = append(imports, match[1])
	}

	// import './styles.scss'
	simpleMatches := regexp.MustCompile(`import\s+['"]([^'"]+)['"]`).FindAllStringSubmatch(string(content), -1)
	for _, match := range simpleMatches {
		imports = append(imports, match[1])
	}

	// import('apps/...')
	dynamicMatches := regexp.MustCompile(`import\s*\(\s*['"]([^'"]+)['"]\s*\)`).FindAllStringSubmatch(string(content), -1)
	for _, match := range dynamicMatches {
		imports = append(imports, match[1])
	}

	// require('apps/user-app')
	requireMatches := regexp.MustCompile(`require\s*\(\s*['"]([^'"]+)['"]\s*\)`).FindAllStringSubmatch(string(content), -1)
	for _, match := range requireMatches {
		imports = append(imports, match[1])
	}

	return imports, nil
}

func getFileWithExtension(absPath string) (string, bool) {
	info, err := os.Stat(absPath)
	if err == nil {
		if info.IsDir() {
			return findIndexFile(absPath)
		}

		ext := filepath.Ext(absPath)

		if cfg.AllowedExtensions[ext] {
			return filepath.Clean(absPath), true
		}
	}

	if fullPath, ok := findFileWithExtension(absPath); ok {
		return fullPath, true
	}

	return "", false
}

func resolveImportPath(importPath, basePath string) (string, bool) {
	importPath = strings.Trim(importPath, `'"`)

	for _, aliasPath := range cfg.Aliases {
		if strings.HasPrefix(importPath, aliasPath) {
			candidate := filepath.Join(cfg.Root, importPath)

			return getFileWithExtension(candidate)
		}
	}

	if strings.HasPrefix(importPath, ".") {
		dir := filepath.Dir(basePath)
		absPath := filepath.Join(dir, importPath)

		return getFileWithExtension(absPath)
	}

	return "", false
}

func findFileWithExtension(basePath string) (string, bool) {
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".scss", ".module.scss"} {
		path := basePath + ext
		if _, err := os.Stat(path); err == nil {
			return filepath.Clean(path), true
		}
	}
	return "", false
}

func findIndexFile(dir string) (string, bool) {
	for _, name := range cfg.IndexCandidates {
		indexPath := filepath.Join(dir, name)
		if _, err := os.Stat(indexPath); err == nil {
			return filepath.Clean(indexPath), true
		}
	}
	return "", false
}

type ImportGraph struct {
	Used    map[string]bool
	Visited map[string]bool
}

func (g *ImportGraph) Traverse(file string) {
	cleanFile := filepath.Clean(file)
	if g.Visited[cleanFile] {
		return
	}
	g.Visited[cleanFile] = true
	g.Used[cleanFile] = true

	imports, err := extractImports(cleanFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Cannot read %s: %v\n", cleanFile, err)
		return
	}

	for _, imp := range imports {
		resolvedPath, found := resolveImportPath(imp, cleanFile)

		if found {
			g.Used[resolvedPath] = true
			g.Traverse(resolvedPath)
		}
	}
}
