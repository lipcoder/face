package sqlitegorm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/libtnb/sqlite"
	"github.com/lipcoder/face/internal/database"
	"gorm.io/gorm"
)

const (
	StudentIdLength         = 10
	FeatureLength           = 512
	FaceSimilarityThreshold = 0.8
	dateLayout              = "2006-01-02"
)

// peopleRow 定义了人员信息表的结构体
type personRow struct {
	ID       int64  `gorm:"primaryKey;autoIncrement"` // primaryKey 主键,autoIncrement 自增
	PersonID string `gorm:"uniqueIndex;not null"`     // uniqueIndex 唯一索引,not null 非空
	Name     string `gorm:"not null"`                 // not null 非空
	Feature  []byte `gorm:"not null"`                 // not null 非空
}

// TableName 告诉 GORM 这个结构体对应的数据库表名是 "people"
func (personRow) TableName() string { return "people" }

// attendanceRow 定义了签到记录表的结构体
type attendanceRow struct {
	ID       int64  `gorm:"primaryKey;autoIncrement"`                                      // 主键，自增
	PersonID string `gorm:"index:attendance_date_signed_at_person_id,priority:3;not null"` // 联合索引第 3 列，非空
	Date     string `gorm:"index:attendance_date_signed_at_person_id,priority:1;not null"` // 联合索引第 1 列，非空
	SignedAt string `gorm:"index:attendance_date_signed_at_person_id,priority:2;not null"` // 联合索引第 2 列，非空
}

// TableName 告诉 GORM 这个结构体对应的数据库表名是 "attendance"
func (attendanceRow) TableName() string { return "attendance" }

type DataBase struct {
	mu       sync.RWMutex
	gormDB   *gorm.DB
	persons  []database.Person
	location *time.Location
}

func Open(path string) (*DataBase, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("数据库路径为空")
	}

	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, fmt.Errorf("加载签到本地时区失败: %w", err)
	}

	// 如果路径中已经包含了查询参数，则使用 & 作为分隔符，否则使用 ? 作为分隔符
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}

	// 使用 GORM 打开 SQLite 数据库，并启用外键约束
	gormDB, err := gorm.Open(
		sqlite.Open(path+separator+"_fk=1"),
		&gorm.Config{},
	)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 获取底层的 *sql.DB 对象，以便设置连接池参数
	sqlDB, err := gormDB.DB()
	if err != nil {
		return nil, fmt.Errorf("获取数据库连接失败: %w", err)
	}

	// 设置最大打开连接数为 1，以确保 SQLite 数据库的线程安全性
	sqlDB.SetMaxOpenConns(1)

	// 数据库还没有初始化时创建表
	// Migrator()是 GORM 负责数据库结构/schema 管理的对象，操作表、列、索引这些结构
	// HasTable()方法用于检查数据库中是否存在指定的表
	if !gormDB.Migrator().HasTable(&personRow{}) || // 如果没有 people 表
		!gormDB.Migrator().HasTable(&attendanceRow{}) { // 如果没有 attendance 表

		if err := gormDB.AutoMigrate( // 自动迁移，创建表，检查已有结构，补充一些缺少的内容
			&personRow{},
			&attendanceRow{},
		); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("初始化数据库失败: %w", err)
		}
	}

	// 将已有人员加载到内存
	var personRows []personRow
	if err := gormDB.Find(&personRows).Error; err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("读取人员数据失败: %w", err)
	}

	resultDB := &DataBase{
		gormDB:   gormDB,
		persons:  make([]database.Person, 0, len(personRows)),
		location: location,
	}

	for _, row := range personRows {
		person, err := decodePerson(row)
		if err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("读取人员 %s 的特征失败: %w", row.PersonID, err)
		}

		resultDB.persons = append(resultDB.persons, person)
	}

	return resultDB, nil
}

func (d *DataBase) Close() error {
	if d == nil || d.gormDB == nil {
		return nil
	}
	sqlDB, err := d.gormDB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (d *DataBase) AddPerson(p *database.Person) error {
	if p == nil || p.PersonID == "" || strings.TrimSpace(p.Name) == "" || len(p.Feature) != FeatureLength {
		return fmt.Errorf("无效的人员信息")
	}
	if len(p.PersonID) != StudentIdLength {
		return fmt.Errorf("学号必须是%d位", StudentIdLength)
	}
	for _, c := range p.PersonID {
		if c < '0' || c > '9' {
			return fmt.Errorf("学号必须全部是数字")
		}
	}
	personRow := personRow{PersonID: p.PersonID, Name: p.Name, Feature: encodeFeature(p.Feature)}
	if err := d.gormDB.Create(&personRow).Error; err != nil {
		return fmt.Errorf("添加人员失败: %w", err)
	}
	// 更新人员ID并添加到内存中,这里的ID之前只是为了让实现可以直接使用接口文件的Person结构体，现在不需要了，但是也留下了
	p.ID = personRow.ID
	d.mu.Lock()
	d.persons = append(d.persons, database.Person{ID: p.ID, PersonID: p.PersonID, Name: p.Name, Feature: append([]float32(nil), p.Feature...)})
	d.mu.Unlock()
	return nil
}

func (d *DataBase) SearchPerson(p *database.Person) (*database.Person, bool, error) {
	if p == nil {
		return nil, false, fmt.Errorf("无效的人员信息")
	}

	if p.PersonID != "" {
		var row personRow

		err := d.gormDB.
			Where("person_id = ?", p.PersonID).
			First(&row).Error

		if err == nil {
			result, err := decodePerson(row)
			if err != nil {
				return nil, false, fmt.Errorf("读取人员特征失败: %w", err)
			}

			return &result, true, nil
		}

		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, fmt.Errorf("按学号查询人员失败: %w", err)
		}
	}

	if p.Name != "" {
		var row personRow

		err := d.gormDB.
			Where("name = ?", p.Name).
			First(&row).Error

		if err == nil {
			result, err := decodePerson(row)
			if err != nil {
				return nil, false, fmt.Errorf("读取人员特征失败: %w", err)
			}

			return &result, true, nil
		}

		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, fmt.Errorf("按姓名查询人员失败: %w", err)
		}
	}

	return nil, false, nil
}

func (d *DataBase) DeletePerson(personID string) (bool, error) {
	if personID == "" {
		return false, fmt.Errorf("学号为空")
	}

	person, found, err := d.SearchPerson(&database.Person{
		PersonID: personID,
	})
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	if err := d.gormDB.Delete(&personRow{}, person.ID).Error; err != nil {
		return false, fmt.Errorf("删除人员失败: %w", err)
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	for i := range d.persons {
		if d.persons[i].ID == person.ID {
			d.persons = append(d.persons[:i], d.persons[i+1:]...)
			break
		}
	}

	return true, nil
}

func (d *DataBase) ListPersons() ([]database.Person, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	persons := make([]database.Person, len(d.persons))
	for i := range d.persons {
		persons[i] = d.persons[i]
		persons[i].Feature = append([]float32(nil), d.persons[i].Feature...)
	}

	return persons, nil
}

func (d *DataBase) UpdatePersonName(personID, name string) (bool, error) {
	name = strings.TrimSpace(name)
	if len(personID) != StudentIdLength || name == "" {
		return false, fmt.Errorf("无效的学号或姓名")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var index = -1
	for i := range d.persons {
		if d.persons[i].PersonID == personID {
			index = i
			break
		}
	}
	if index < 0 {
		return false, nil
	}
	if err := d.gormDB.Model(&personRow{}).Where("person_id = ?", personID).Update("name", name).Error; err != nil {
		return false, fmt.Errorf("修改姓名失败: %w", err)
	}
	d.persons[index].Name = name
	return true, nil
}

// encodeFeature 将 []float32 转换为 []byte
func encodeFeature(feature []float32) []byte {
	data := make([]byte, len(feature)*4)
	for i, value := range feature {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(value))
	}
	return data
}

// decodePerson 将 personRow 转换为 database.Person
func decodePerson(row personRow) (database.Person, error) {
	if len(row.Feature) != FeatureLength*4 {
		return database.Person{}, fmt.Errorf("特征数据长度错误: got=%d want=%d", len(row.Feature), FeatureLength*4)
	}
	feature := make([]float32, FeatureLength)
	for i := range feature {
		feature[i] = math.Float32frombits(binary.LittleEndian.Uint32(row.Feature[i*4:]))
	}
	return database.Person{ID: row.ID, PersonID: row.PersonID, Name: row.Name, Feature: feature}, nil
}
