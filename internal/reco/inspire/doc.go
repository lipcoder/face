// 提供识别服务
// 
// 但是存在一些问题：
// trackid会随着时间推移一直增加，若长时间运行，map[int64]reco.FaceInfo会越来越大，可能导致内存占用过高
package inspire
