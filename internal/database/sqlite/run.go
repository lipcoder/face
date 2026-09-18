package sqlite

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/lipcoder/face/internal/database"
	_ "modernc.org/sqlite"
)

type DataBase struct {
	db *sql.DB

	mu     sync.RWMutex
	people []database.Person
}

// 初始化数据库
const schema = `
CREATE TABLE IF NOT EXISTS people (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    person_id  TEXT    NOT NULL UNIQUE,
    name       TEXT    NOT NULL CHECK(length(trim(name)) > 0),
    feature    BLOB    NOT NULL CHECK(length(feature) = 2048)
) STRICT;
`

func Init(DataBasePath string) (*DataBase, error) {
	if strings.TrimSpace(DataBasePath) == "" {
		return nil, fmt.Errorf("数据库路径为空")
	}

	db, err := sql.Open("sqlite", DataBasePath)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 设置最大连接数为1，确保线程安全
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}

	d := &DataBase{
		db:     db,
		people: make([]database.Person, 0),
	}

	rows, err := d.db.Query(
		"SELECT id, person_id, name, feature FROM people",
	)
	if err != nil {
		return nil, fmt.Errorf("读取人员数据失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var person database.Person
		var featureData []byte

		if err := rows.Scan(
			&person.ID,
			&person.PersonID,
			&person.Name,
			&featureData,
		); err != nil {
			return nil, fmt.Errorf("读取人员失败: %w", err)
		}

		d.people = append(d.people, person)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取人员数据失败: %w", err)
	}

	return d, nil
}

func (d *DataBase) Close() error {
	if d == nil || d.db == nil {
		return nil
	}
	return d.db.Close()
}

// AddPerson 添加人员信息到数据库
//
// 当前实现中，写死了学号长度为10位，姓名不能为空，特征向量必须为512维
func (d *DataBase) AddPerson(person *database.Person) error {
	if person == nil || person.PersonID == "" || person.Name == "" ||
		len(person.Feature) != database.FeatureLength {
		return fmt.Errorf("无效的人员信息")
	}

	if len(person.PersonID) != database.StudentIDLength {
		return fmt.Errorf("学号必须是%d位", database.StudentIDLength)
	}
	for _, c := range person.PersonID {
		if c < '0' || c > '9' {
			return fmt.Errorf("学号必须全部是数字")
		}
	}

	result, err := d.db.Exec(
		"INSERT INTO people (person_id, name, feature) VALUES (?, ?, ?)",
		person.PersonID, person.Name, person.Feature,
	)
	if err != nil {
		return fmt.Errorf("添加人员失败: %w", err)
	}

	person.ID, err = result.LastInsertId()
	if err != nil {
		return fmt.Errorf("获取新插入的人员ID失败: %w", err)
	}

	// SQLite 添加成功后，同步加入内存
	d.people = append(d.people, *person)

	return nil
}

func (d *DataBase) DeletePerson(person *database.Person) error {
	if person == nil {
		return fmt.Errorf("无效的人员信息")
	}

	if person.PersonID != "" {
		result, err := d.db.Exec("DELETE FROM people WHERE person_id = ?", person.PersonID)
		if err != nil {
			return fmt.Errorf("删除人员失败: %w", err)
		}
		if rowsAffected, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("获取删除行数失败: %w", err)
		} else if rowsAffected == 0 {
			return fmt.Errorf("未找到学号为 %s 的人员", person.PersonID)
		}
	}

	for i := range d.people {
		if d.people[i].PersonID == person.PersonID {
			d.people = append(
				d.people[:i],
				d.people[i+1:]...,
			)
			break
		}
	}

	return nil
}

func (d *DataBase) SearchPerson(person *database.Person) (*database.Person, error) {
	if person == nil {
		return nil, fmt.Errorf("无效的人员信息")
	}

	if person.PersonID != "" {
		row := d.db.QueryRow("SELECT id, person_id, name, feature FROM people WHERE person_id = ?", person.PersonID)
		var p database.Person
		if err := row.Scan(&p.ID, &p.PersonID, &p.Name, &p.Feature); err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("未找到学号为 %s 的人员", person.PersonID)
			}
			return nil, fmt.Errorf("查询人员失败: %w", err)
		}
		return &p, nil
	}

	if person.Name != "" {
		row := d.db.QueryRow("SELECT id, person_id, name, feature FROM people WHERE name = ?", person.Name)
		var p database.Person
		if err := row.Scan(&p.ID, &p.PersonID, &p.Name, &p.Feature); err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("未找到姓名为 %s 的人员", person.Name)
			}
			return nil, fmt.Errorf("查询人员失败: %w", err)
		}
		return &p, nil
	}

	if len(person.Feature) == database.FeatureLength {
		return d.SearchByFeature(person.Feature)
	}

	return nil, fmt.Errorf("未提供有效的查询条件")
}

func (d *DataBase) SearchByFeature(feature []float32) (*database.Person, error) {
	if len(feature) != database.FeatureLength {
		return nil, fmt.Errorf("无效的人脸特征")
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	var best *database.Person
	var bestSimilarity float32 = -1

	for i := range d.people {
		similarity := cosineSimilarity(
			feature,
			d.people[i].Feature,
		)

		if similarity > bestSimilarity {
			bestSimilarity = similarity
			best = &d.people[i]
		}
	}

	if best == nil || bestSimilarity < database.FaceSimilarityThreshold {
		return nil, database.ErrNotFound
	}

	result := *best
	return &result, nil
}

func cosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}

	var dot float64
	var normA float64
	var normB float64

	for i := range a {
		av := float64(a[i])
		bv := float64(b[i])

		dot += av * bv
		normA += av * av
		normB += bv * bv
	}

	// 防止零向量导致除 0
	if normA == 0 || normB == 0 {
		return 0
	}

	similarity := dot / (math.Sqrt(normA) * math.Sqrt(normB))

	// 防止浮点误差出现 1.00000001 或 -1.00000001
	if similarity > 1 {
		similarity = 1
	} else if similarity < -1 {
		similarity = -1
	}

	return float32(similarity)
}

func (d *DataBase) ListPeople() ([]database.Person, error) {
	return d.people, nil
}
