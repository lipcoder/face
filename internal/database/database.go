// Package database 定义本地人脸特征库的接口。
package database

type Person struct {
	ID       int64
	PersonID string
	Name     string
	Feature  []float32
}

// Date 为本地日期（YYYY-MM-DD）
// SignedAt 为带本地时区偏移的 RFC3339 时间
type Attendance struct {
	PersonID string
	SignDate string
	SignedAt string
}

type Database interface {
	// AddPerson 添加人员信息到数据库
	AddPerson(person *Person) error
	ListPersons() ([]Person, error)
	UpdatePersonName(personID, name string) (bool, error)
	// DeletePerson 删除人员信息
	DeletePerson(personID string) (bool, error)
	// SearchPerson 查找人员信息
	SearchPerson(person *Person) (*Person, bool, error)
	// SearchByFeature 根据特征向量查找人员信息
	SearchByFeature(feature []float32) (*Person, bool, error)

	// 签到
	Sign(personID string) (bool, error)
	// 查询某人的签到记录
	GetAttendanceByPerson(personID string) ([]Attendance, error)
	// 查询某日的签到记录
	GetAttendanceByDate(date string) ([]Attendance, error)
	// 关闭数据库连接
	Close() error
}
