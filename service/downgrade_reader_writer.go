package service

import "io"

var _ io.ReadWriteCloser = (*DowngradeReaderWriter)(nil)

type DowngradeReaderWriter struct {
	io.ReadWriteCloser
}

func NewDowngradeReaderWriter(rwc io.ReadWriteCloser) DowngradeReaderWriter {
	return DowngradeReaderWriter{ReadWriteCloser: rwc}
}
