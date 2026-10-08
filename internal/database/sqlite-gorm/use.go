package sqlitegorm

import (
	"fmt"
	"time"

	"github.com/lipcoder/face/internal/database"
)

func (d *DataBase) Sign(personID string) (bool, error) {
	person, found, err := d.SearchPerson(&database.Person{
		PersonID: personID,
	})
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	local := time.Now().In(d.location)

	row := attendanceRow{
		PersonID: person.PersonID,
		Date:     local.Format(dateLayout),
		SignedAt: local.Format(time.RFC3339Nano),
	}

	if err := d.gormDB.Create(&row).Error; err != nil {
		return false, fmt.Errorf("写入签到记录失败: %w", err)
	}

	return true, nil
}

func (d *DataBase) GetAttendanceByPerson(personID string) ([]database.Attendance, error) {
	if personID == "" {
		return nil, fmt.Errorf("学号为空")
	}

	var attendanceRows []attendanceRow
	if err := d.gormDB.
		Where("person_id = ?", personID).
		Order("date, signed_at").
		Find(&attendanceRows).Error; err != nil { // find 为查找所有记录，如果没有会输出空切片，不会返回错误
		return nil, fmt.Errorf("查询签到记录失败: %w", err)
	}
	return attendanceRecords(attendanceRows), nil
}

func (d *DataBase) GetAttendanceByDate(date string) ([]database.Attendance, error) {
	parsed, err := time.Parse(dateLayout, date)
	if err != nil || parsed.Format(dateLayout) != date {
		return nil, fmt.Errorf("无效的日期 %q，要求 YYYY-MM-DD", date)
	}
	var attendanceRows []attendanceRow
	if err := d.gormDB.
		Where("date = ?", date).
		Order("signed_at, person_id").
		Find(&attendanceRows).Error; err != nil {
		return nil, fmt.Errorf("查询签到记录失败: %w", err)
	}
	return attendanceRecords(attendanceRows), nil
}

func (d *DataBase) GetAttendanceByRange(start, end string) ([]database.Attendance, error) {
	first, err := time.Parse(dateLayout, start)
	if err != nil || first.Format(dateLayout) != start {
		return nil, fmt.Errorf("无效的开始日期")
	}
	last, err := time.Parse(dateLayout, end)
	if err != nil || last.Format(dateLayout) != end || last.Before(first) {
		return nil, fmt.Errorf("无效的结束日期")
	}
	var rows []attendanceRow
	if err := d.gormDB.Where("date >= ? AND date <= ?", start, end).
		Order("signed_at DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("查询签到记录失败: %w", err)
	}
	return attendanceRecords(rows), nil
}

// attendanceRecords 将 []attendanceRow 转换为 []database.Attendance
func attendanceRecords(rows []attendanceRow) []database.Attendance {
	records := make([]database.Attendance, 0, len(rows))
	for _, row := range rows {
		records = append(records, database.Attendance{PersonID: row.PersonID, SignDate: row.Date, SignedAt: row.SignedAt})
	}
	return records
}
