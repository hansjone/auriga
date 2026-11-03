// Package relaylimits defines transport bounds shared by the member bridge and
// relay. These bounds protect JSON parsing and do not express model capacity.
package relaylimits

const ChatBodyBytes int64 = 32 << 20
