package mongoreview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/platform/database"
)

var (
	ErrNotFound      = errors.New("mongodb review record not found")
	ErrInvalidInput  = errors.New("invalid mongodb review input")
	ErrNotConfigured = errors.New("mongodb review is not configured")
)

var repositoryTaskPattern = regexp.MustCompile(`(?i)^(MCC-[0-9]+)`)

type Service struct {
	db             *database.DB
	analyzer       *AnalyzerClient
	cipher         *credentialCipher
	cipherErr      error
	repositoryRoot string
	reviews        *reviewManager
}

func NewService(db *database.DB, analyzerURL, repositoryRoot, encryptionKey string) *Service {
	cipher, cipherErr := newCredentialCipher(encryptionKey)
	service := &Service{
		db:             db,
		analyzer:       NewAnalyzerClient(analyzerURL),
		cipher:         cipher,
		cipherErr:      cipherErr,
		repositoryRoot: filepath.Clean(repositoryRoot),
	}
	service.reviews = newReviewManager(service)
	return service
}

func (s *Service) AnalyzerHealth(ctx context.Context) error {
	return s.analyzer.Health(ctx)
}

func (s *Service) ListEnvironments(ctx context.Context) ([]Environment, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select environment, database_name, updated_at
		from mongodb_review_environments
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byName := map[string]Environment{}
	for rows.Next() {
		var item Environment
		if err := rows.Scan(&item.Environment, &item.DatabaseName, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.Configured = true
		byName[item.Environment] = item
	}
	result := make([]Environment, 0, len(ValidEnvironments))
	for _, name := range []string{"demo", "test", "stag", "prod"} {
		item := byName[name]
		item.Environment = name
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) SaveEnvironment(ctx context.Context, name string, input EnvironmentInput) (Environment, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !ValidEnvironments[name] {
		return Environment{}, fmt.Errorf("%w: unsupported environment", ErrInvalidInput)
	}
	if strings.TrimSpace(input.ConnectionURI) == "" || strings.TrimSpace(input.DatabaseName) == "" {
		return Environment{}, fmt.Errorf("%w: connection_uri and database_name are required", ErrInvalidInput)
	}
	if s.cipherErr != nil {
		return Environment{}, fmt.Errorf("%w: %v", ErrNotConfigured, s.cipherErr)
	}
	encrypted, err := s.cipher.Encrypt(strings.TrimSpace(input.ConnectionURI))
	if err != nil {
		return Environment{}, err
	}
	var item Environment
	err = s.db.Pool.QueryRow(ctx, `
		insert into mongodb_review_environments (environment, connection_uri_cipher, database_name, updated_at)
		values ($1, $2, $3, now())
		on conflict (environment) do update set
			connection_uri_cipher = excluded.connection_uri_cipher,
			database_name = excluded.database_name,
			updated_at = now()
		returning environment, database_name, updated_at
	`, name, encrypted, strings.TrimSpace(input.DatabaseName)).Scan(
		&item.Environment, &item.DatabaseName, &item.UpdatedAt,
	)
	item.Configured = err == nil
	return item, err
}

func (s *Service) environmentCredential(ctx context.Context, name string) (string, string, error) {
	if s.cipherErr != nil {
		return "", "", fmt.Errorf("%w: %v", ErrNotConfigured, s.cipherErr)
	}
	var encrypted []byte
	var databaseName string
	err := s.db.Pool.QueryRow(ctx, `
		select connection_uri_cipher, database_name
		from mongodb_review_environments where environment = $1
	`, name).Scan(&encrypted, &databaseName)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotConfigured
	}
	if err != nil {
		return "", "", err
	}
	uri, err := s.cipher.Decrypt(encrypted)
	return uri, databaseName, err
}

func (s *Service) TestEnvironment(ctx context.Context, name string, input EnvironmentInput) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if !ValidEnvironments[name] {
		return fmt.Errorf("%w: unsupported environment", ErrInvalidInput)
	}

	uri := strings.TrimSpace(input.ConnectionURI)
	databaseName := strings.TrimSpace(input.DatabaseName)
	if (uri == "") != (databaseName == "") {
		return fmt.Errorf(
			"%w: connection_uri and database_name must be provided together",
			ErrInvalidInput,
		)
	}
	if uri == "" {
		var err error
		uri, databaseName, err = s.environmentCredential(ctx, name)
		if err != nil {
			return err
		}
	}
	client, err := connectReadOnlyMongo(ctx, uri, databaseName)
	if err != nil {
		// Driver errors must not reach an API response because malformed
		// connection strings can contain credentials in their error text.
		return errors.New("MongoDB connection test failed")
	}
	closeMongo(client)
	return nil
}

func validateRule(rule QueryRule) error {
	if strings.TrimSpace(rule.Name) == "" || strings.TrimSpace(rule.Collection) == "" {
		return fmt.Errorf("%w: rule name and collection are required", ErrInvalidInput)
	}
	if len(rule.FieldMappings) == 0 {
		return fmt.Errorf("%w: at least one field mapping is required", ErrInvalidInput)
	}
	for _, mapping := range rule.FieldMappings {
		if !validFieldPath(mapping.DocumentPath) || !validFieldPath(mapping.QueryField) {
			return fmt.Errorf("%w: invalid field mapping", ErrInvalidInput)
		}
	}
	return nil
}

func (s *Service) ListRules(ctx context.Context) ([]QueryRule, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, name, collection_name, field_mappings, created_at, updated_at
		from mongodb_review_query_rules order by collection_name, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]QueryRule, 0)
	for rows.Next() {
		var item QueryRule
		var mappings []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Collection, &mappings, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(mappings, &item.FieldMappings); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) SaveRule(ctx context.Context, input QueryRule) (QueryRule, error) {
	input.ID = strings.TrimSpace(input.ID)
	if input.ID == "" {
		input.ID = uuid.NewString()
	}
	if _, err := uuid.Parse(input.ID); err != nil {
		return QueryRule{}, fmt.Errorf("%w: invalid rule id", ErrInvalidInput)
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Collection = strings.TrimSpace(input.Collection)
	if err := validateRule(input); err != nil {
		return QueryRule{}, err
	}
	mappings, _ := json.Marshal(input.FieldMappings)
	err := s.db.Pool.QueryRow(ctx, `
		insert into mongodb_review_query_rules (id, name, collection_name, field_mappings)
		values ($1, $2, $3, $4)
		on conflict (id) do update set
			name = excluded.name,
			collection_name = excluded.collection_name,
			field_mappings = excluded.field_mappings,
			updated_at = now()
		returning created_at, updated_at
	`, input.ID, input.Name, input.Collection, mappings).Scan(&input.CreatedAt, &input.UpdatedAt)
	return input, err
}

func (s *Service) DeleteRule(ctx context.Context, id string) error {
	tag, err := s.db.Pool.Exec(ctx, `delete from mongodb_review_query_rules where id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) rule(ctx context.Context, id string) (QueryRule, error) {
	var item QueryRule
	var mappings []byte
	err := s.db.Pool.QueryRow(ctx, `
		select id::text, name, collection_name, field_mappings, created_at, updated_at
		from mongodb_review_query_rules where id = $1
	`, id).Scan(&item.ID, &item.Name, &item.Collection, &mappings, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return QueryRule{}, ErrNotFound
	}
	if err != nil {
		return QueryRule{}, err
	}
	err = json.Unmarshal(mappings, &item.FieldMappings)
	return item, err
}

func (s *Service) Parse(ctx context.Context, source string) (ParseResult, error) {
	if len(source) == 0 || len(source) > MaxScriptBytes {
		return ParseResult{}, fmt.Errorf("%w: script must contain between 1 byte and 4 MiB", ErrInvalidInput)
	}
	return s.analyzer.Parse(ctx, source)
}

func (s *Service) SaveScript(ctx context.Context, input Script) (Script, error) {
	input.ID = strings.TrimSpace(input.ID)
	if input.ID == "" {
		input.ID = uuid.NewString()
	}
	if _, err := uuid.Parse(input.ID); err != nil {
		return Script{}, fmt.Errorf("%w: invalid script id", ErrInvalidInput)
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" || len(input.Source) == 0 || len(input.Source) > MaxScriptBytes {
		return Script{}, fmt.Errorf("%w: title and source are required", ErrInvalidInput)
	}
	parsed, err := s.Parse(ctx, input.Source)
	if err != nil {
		return Script{}, err
	}
	for index := range parsed.Operations {
		if description := strings.TrimSpace(input.OperationDescriptions[parsed.Operations[index].ID]); description != "" {
			parsed.Operations[index].Description = description
		}
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return Script{}, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `
		insert into mongodb_review_scripts (id, title, source, origin_path)
		values ($1, $2, $3, $4)
		on conflict (id) do update set
			title = excluded.title,
			source = excluded.source,
			origin_path = excluded.origin_path,
			updated_at = now()
		returning created_at, updated_at
	`, input.ID, input.Title, input.Source, input.OriginPath).Scan(&input.CreatedAt, &input.UpdatedAt)
	if err != nil {
		return Script{}, err
	}
	if _, err := tx.Exec(ctx, `delete from mongodb_review_operations where script_id = $1`, input.ID); err != nil {
		return Script{}, err
	}
	for index, operation := range parsed.Operations {
		payload, _ := json.Marshal(operation)
		if _, err := tx.Exec(ctx, `
			insert into mongodb_review_operations (
				id, script_id, operation_index, operation_type, collection_name, description, parse_payload
			) values ($1, $2, $3, $4, $5, $6, $7)
		`, input.ID+"-"+operation.ID, input.ID, index, operation.Type, operation.Collection, operation.Description, payload); err != nil {
			return Script{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Script{}, err
	}
	input.Operations = parsed.Operations
	return input, nil
}

func (s *Service) ListScripts(ctx context.Context) ([]Script, error) {
	rows, err := s.db.Pool.Query(ctx, `
		select id::text, title, source, origin_path, created_at, updated_at
		from mongodb_review_scripts order by updated_at desc
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Script, 0)
	for rows.Next() {
		var item Script
		if err := rows.Scan(&item.ID, &item.Title, &item.Source, &item.OriginPath, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Service) GetScript(ctx context.Context, id string) (Script, error) {
	var item Script
	err := s.db.Pool.QueryRow(ctx, `
		select id::text, title, source, origin_path, created_at, updated_at
		from mongodb_review_scripts where id = $1
	`, id).Scan(&item.ID, &item.Title, &item.Source, &item.OriginPath, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Script{}, ErrNotFound
	}
	if err != nil {
		return Script{}, err
	}
	rows, err := s.db.Pool.Query(ctx, `
		select parse_payload from mongodb_review_operations
		where script_id = $1 order by operation_index
	`, id)
	if err != nil {
		return Script{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return Script{}, err
		}
		var operation ParsedOperation
		if err := json.Unmarshal(payload, &operation); err != nil {
			return Script{}, err
		}
		item.Operations = append(item.Operations, operation)
	}
	return item, rows.Err()
}

func (s *Service) DeleteScript(ctx context.Context, id string) error {
	tag, err := s.db.Pool.Exec(ctx, `delete from mongodb_review_scripts where id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) ListRepositoryFiles() ([]RepositoryFile, error) {
	root, err := filepath.EvalSymlinks(s.repositoryRoot)
	if err != nil {
		return nil, fmt.Errorf("repository root: %w", err)
	}
	result := make([]RepositoryFile, 0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(strings.ToLower(entry.Name()), ".js") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > MaxScriptBytes {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result = append(result, RepositoryFile{
			Path: filepath.ToSlash(relative), Size: info.Size(), ModifiedAt: info.ModTime(),
		})
		if len(result) >= 2000 {
			return fs.SkipAll
		}
		return nil
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, err
}

func repositoryTaskKey(folder string) string {
	match := repositoryTaskPattern.FindStringSubmatch(folder)
	if len(match) != 2 {
		return ""
	}
	return strings.ToUpper(match[1])
}

func (s *Service) repositoryModulesRoot() (string, error) {
	root, err := filepath.EvalSymlinks(s.repositoryRoot)
	if err != nil {
		return "", fmt.Errorf("repository root: %w", err)
	}
	modules, err := filepath.EvalSymlinks(filepath.Join(root, "modules"))
	if err != nil {
		return "", fmt.Errorf("repository modules: %w", err)
	}
	if modules == root || !strings.HasPrefix(modules, root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: modules path escapes repository root", ErrInvalidInput)
	}
	return modules, nil
}

func countRepositoryJSFiles(root string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink == 0 && strings.HasSuffix(strings.ToLower(entry.Name()), ".js") {
			count++
		}
		return nil
	})
	return count, err
}

func (s *Service) ListRepositoryIndex() ([]RepositoryProject, []RepositoryTaskSummary, error) {
	modules, err := s.repositoryModulesRoot()
	if err != nil {
		return nil, nil, err
	}
	projectEntries, err := os.ReadDir(modules)
	if err != nil {
		return nil, nil, err
	}
	projects := make([]RepositoryProject, 0)
	taskMap := map[string]*RepositoryTaskSummary{}
	for _, projectEntry := range projectEntries {
		if !projectEntry.IsDir() || projectEntry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(projectEntry.Name(), ".") {
			continue
		}
		projectPath := filepath.Join(modules, projectEntry.Name())
		taskEntries, err := os.ReadDir(projectPath)
		if err != nil {
			return nil, nil, err
		}
		project := RepositoryProject{Name: projectEntry.Name(), TaskFolders: make([]string, 0)}
		for _, taskEntry := range taskEntries {
			if !taskEntry.IsDir() || taskEntry.Type()&os.ModeSymlink != 0 {
				continue
			}
			key := repositoryTaskKey(taskEntry.Name())
			if key == "" {
				continue
			}
			fileCount, err := countRepositoryJSFiles(filepath.Join(projectPath, taskEntry.Name()))
			if err != nil {
				return nil, nil, err
			}
			project.TaskFolders = append(project.TaskFolders, taskEntry.Name())
			task := taskMap[key]
			if task == nil {
				task = &RepositoryTaskSummary{Key: key, Locations: make([]RepositoryTaskLocation, 0)}
				taskMap[key] = task
			}
			task.Locations = append(task.Locations, RepositoryTaskLocation{
				Project: project.Name, TaskFolder: taskEntry.Name(), FileCount: fileCount,
			})
			task.FileCount += fileCount
		}
		sort.Strings(project.TaskFolders)
		projects = append(projects, project)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	tasks := make([]RepositoryTaskSummary, 0, len(taskMap))
	for _, task := range taskMap {
		sort.Slice(task.Locations, func(i, j int) bool {
			if task.Locations[i].Project == task.Locations[j].Project {
				return task.Locations[i].TaskFolder < task.Locations[j].TaskFolder
			}
			return task.Locations[i].Project < task.Locations[j].Project
		})
		tasks = append(tasks, *task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Key < tasks[j].Key })
	return projects, tasks, nil
}

func (s *Service) GetRepositoryTask(ctx context.Context, requestedKey string) (RepositoryTask, error) {
	key := strings.ToUpper(strings.TrimSpace(requestedKey))
	if repositoryTaskKey(key) != key {
		return RepositoryTask{}, fmt.Errorf("%w: invalid task key", ErrInvalidInput)
	}
	modules, err := s.repositoryModulesRoot()
	if err != nil {
		return RepositoryTask{}, err
	}
	_, summaries, err := s.ListRepositoryIndex()
	if err != nil {
		return RepositoryTask{}, err
	}
	var summary *RepositoryTaskSummary
	for index := range summaries {
		if summaries[index].Key == key {
			summary = &summaries[index]
			break
		}
	}
	if summary == nil {
		return RepositoryTask{}, ErrNotFound
	}
	result := RepositoryTask{
		Key: key, Locations: summary.Locations, Files: make([]RepositoryTaskFile, 0),
	}
	for _, location := range summary.Locations {
		taskRoot := filepath.Join(modules, location.Project, location.TaskFolder)
		err := filepath.WalkDir(taskRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path != taskRoot && strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(strings.ToLower(entry.Name()), ".js") {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Size() > MaxScriptBytes {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			parsed, err := s.Parse(ctx, string(content))
			if err != nil {
				return err
			}
			relative, err := filepath.Rel(modules, path)
			if err != nil {
				return err
			}
			file := RepositoryTaskFile{
				Path:    filepath.ToSlash(filepath.Join("modules", relative)),
				Project: location.Project, TaskFolder: location.TaskFolder,
				Size: info.Size(), ModifiedAt: info.ModTime(), Statements: make([]RepositoryStatement, 0),
				Diagnostics: parsed.Diagnostics,
			}
			for index, operation := range parsed.Operations {
				statementSource := operation.ContextSource
				if statementSource == "" {
					statementSource = operation.Source
				}
				file.Statements = append(file.Statements, RepositoryStatement{
					ID: fmt.Sprintf("%s#%s", file.Path, operation.ID), Index: index,
					Source: statementSource, Operation: operation, Project: location.Project,
					TaskFolder: location.TaskFolder, FilePath: file.Path,
				})
			}
			result.Files = append(result.Files, file)
			return nil
		})
		if err != nil {
			return RepositoryTask{}, err
		}
	}
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].Path < result.Files[j].Path })
	return result, nil
}

func validRepositorySegment(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "." && value != ".." &&
		!strings.ContainsAny(value, `/\`) && !strings.HasPrefix(value, ".")
}

func ensureRepositoryPathHasNoSymlink(base, target string) error {
	relative, err := filepath.Rel(base, target)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: path escapes task folder", ErrInvalidInput)
	}
	current := base
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symbolic links are not allowed", ErrInvalidInput)
		}
	}
	return nil
}

func (s *Service) CreateRepositoryFile(input CreateRepositoryFileInput) (RepositoryFile, error) {
	input.Project = strings.TrimSpace(input.Project)
	input.TaskFolder = strings.TrimSpace(input.TaskFolder)
	input.FilePath = strings.TrimSpace(input.FilePath)
	if !validRepositorySegment(input.Project) || !validRepositorySegment(input.TaskFolder) ||
		repositoryTaskKey(input.TaskFolder) == "" {
		return RepositoryFile{}, fmt.Errorf("%w: invalid project or MCC task folder", ErrInvalidInput)
	}
	if len(input.Source) == 0 || len(input.Source) > MaxScriptBytes {
		return RepositoryFile{}, fmt.Errorf("%w: source must contain between 1 byte and 4 MiB", ErrInvalidInput)
	}
	if input.FilePath == "" || strings.Contains(input.FilePath, `\`) {
		return RepositoryFile{}, fmt.Errorf("%w: invalid JavaScript file path", ErrInvalidInput)
	}
	cleanFile := filepath.Clean(filepath.FromSlash(input.FilePath))
	if filepath.IsAbs(cleanFile) || cleanFile == "." || cleanFile == ".." ||
		strings.HasPrefix(cleanFile, ".."+string(filepath.Separator)) ||
		!strings.HasSuffix(strings.ToLower(cleanFile), ".js") {
		return RepositoryFile{}, fmt.Errorf("%w: file_path must be a relative .js path", ErrInvalidInput)
	}
	modules, err := s.repositoryModulesRoot()
	if err != nil {
		return RepositoryFile{}, err
	}
	projectPath := filepath.Join(modules, input.Project)
	projectInfo, err := os.Lstat(projectPath)
	if err != nil || !projectInfo.IsDir() || projectInfo.Mode()&os.ModeSymlink != 0 {
		return RepositoryFile{}, fmt.Errorf("%w: project folder does not exist", ErrInvalidInput)
	}
	taskPath := filepath.Join(projectPath, input.TaskFolder)
	if err := ensureRepositoryPathHasNoSymlink(projectPath, taskPath); err != nil {
		return RepositoryFile{}, err
	}
	if err := os.Mkdir(taskPath, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return RepositoryFile{}, err
	}
	taskInfo, err := os.Lstat(taskPath)
	if err != nil || !taskInfo.IsDir() || taskInfo.Mode()&os.ModeSymlink != 0 {
		return RepositoryFile{}, fmt.Errorf("%w: task folder is not a regular directory", ErrInvalidInput)
	}
	target := filepath.Join(taskPath, cleanFile)
	parent := filepath.Dir(target)
	if err := ensureRepositoryPathHasNoSymlink(taskPath, parent); err != nil {
		return RepositoryFile{}, err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return RepositoryFile{}, err
	}
	if err := ensureRepositoryPathHasNoSymlink(taskPath, target); err != nil {
		return RepositoryFile{}, err
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return RepositoryFile{}, fmt.Errorf("%w: repository file already exists", ErrInvalidInput)
	}
	if err != nil {
		return RepositoryFile{}, err
	}
	if _, err := file.WriteString(input.Source); err != nil {
		_ = file.Close()
		return RepositoryFile{}, err
	}
	if err := file.Close(); err != nil {
		return RepositoryFile{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return RepositoryFile{}, err
	}
	relative, _ := filepath.Rel(modules, target)
	return RepositoryFile{
		Path: filepath.ToSlash(filepath.Join("modules", relative)),
		Size: info.Size(), ModifiedAt: info.ModTime(),
	}, nil
}

func (s *Service) ReadRepositoryFile(relative string) ([]byte, error) {
	root, err := filepath.EvalSymlinks(s.repositoryRoot)
	if err != nil {
		return nil, err
	}
	candidate := filepath.Join(root, filepath.FromSlash(strings.TrimSpace(relative)))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return nil, ErrNotFound
	}
	prefix := root + string(filepath.Separator)
	if resolved == root || !strings.HasPrefix(resolved, prefix) || !strings.HasSuffix(strings.ToLower(resolved), ".js") {
		return nil, fmt.Errorf("%w: path escapes repository root", ErrInvalidInput)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxScriptBytes {
		return nil, ErrNotFound
	}
	return os.ReadFile(resolved)
}

func (s *Service) StartReview(ctx context.Context, input ReviewRequest) (Review, error) {
	return s.reviews.Start(ctx, input)
}

func (s *Service) GetReview(id string) (Review, error) {
	return s.reviews.Get(id)
}

func (s *Service) SubscribeReview(id string, after int64) (<-chan ReviewEvent, func(), error) {
	return s.reviews.Subscribe(id, after)
}
