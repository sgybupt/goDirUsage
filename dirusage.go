package godirusage

import (
	"encoding/binary"
	"golang.org/x/sys/unix"
	"log"
	"path"
	"unsafe"
)

const (
	InoOff  = unsafe.Offsetof(Dirent{}.Ino)
	RecOff  = unsafe.Offsetof(Dirent{}.Reclen)
	NameOff = unsafe.Offsetof(Dirent{}.Name)

	InoSize  = unsafe.Sizeof(Dirent{}.Ino)
	RecSize  = unsafe.Sizeof(Dirent{}.Reclen)
	NameSize = unsafe.Sizeof(Dirent{}.Name)
)

type FileSystemUsageStruct struct {
	AvailSize uint64 `json:"avail_size"`
	UsedSize  uint64 `json:"used_size"`
	AllSize   uint64 `json:"all_size"`
}

func GetFileSystem(path string) (FreeSpaceInfo FileSystemUsageStruct, err error) {
	sfs := unix.Statfs_t{}
	if err = unix.Statfs(path, &sfs); err != nil {
		return FreeSpaceInfo, err
	}
	bSize := uint64(sfs.Bsize)
	FreeSpaceInfo.AvailSize = sfs.Bavail * bSize
	FreeSpaceInfo.AllSize = sfs.Blocks * bSize
	FreeSpaceInfo.UsedSize = FreeSpaceInfo.AllSize - FreeSpaceInfo.AvailSize
	return FreeSpaceInfo, nil
}

func isDir(mode uint32) bool {
	return mode&unix.S_IFDIR == uint32(16384) // 16384 = 2^14 is equal to >> 15 == 1
}

// modify buf in this function, which minus consume buf.
func parseDirent(buf []byte, names []string, fileNumber int64) (_ []string, _ error) {
	for uintptr(len(buf)) > RecOff+RecSize && fileNumber != 0 {
		recLen := binary.LittleEndian.Uint16(buf[RecOff : RecOff+RecSize])

		rec := buf[:recLen]
		buf = buf[recLen:] // remove head struct here.

		ino := binary.LittleEndian.Uint64(rec[InoOff : InoOff+InoSize])
		if ino == 0 { // File absent in directory.
			continue
		}

		nameBuf := rec[NameOff:]

		if nameBufLen := len(nameBuf); nameBufLen <= 8 {
			for i, b := range nameBuf {
				if b == 0 {
					nameBuf = nameBuf[:i]
					break
				}
			}
		} else {
			for i, b := range nameBuf[nameBufLen-8:] {
				if b == 0 {
					nameBuf = nameBuf[:nameBufLen-8+i]
					break
				}
			}
		}
		name := string(nameBuf)
		if name == "." || name == ".." {
			continue
		}
		fileNumber--
		names = append(names, name)
	}
	return names, nil
}

// file -> nil, size, false, nil
// dir -> [...], 0, true, nil
func fileCheck(fp string) (names []string, size int64, dir bool, err error) {
	fd, err := unix.Open(fp, unix.O_RDONLY, 0664) // check file mode
	if err != nil {
		log.Println("open file err:", fp, []byte(fp), err)
		return names, size, dir, err
	}
	defer unix.Close(fd)
	fStat := unix.Stat_t{}
	if err = unix.Fstat(fd, &fStat); err != nil {
		log.Println("Fstat err:", err)
		return names, size, dir, err
	}
	dir = isDir(uint32(fStat.Mode))
	if !dir {
		return names, fStat.Size, dir, nil
	}
	names = make([]string, 0, 256) // len 0 cap 256
	buf := make([]byte, 32*1024)   // len = 32K
	for {
		// 此处bufferLen是有效长度, 如果buf尾处不够一个完整读取, 则systemcall不会将最后的recBuff放在尾部 而会放在下一次读取的开头
		// buf 每一次都是完整dirent的读取
		bufLen, err := unix.ReadDirent(fd, buf)
		if bufLen == 0 {
			break
		}
		if err != nil {
			log.Println("read dirent err:", err)
			return names, size, dir, err
		}
		buf = buf[:bufLen]
		names, err = parseDirent(buf, names, -1)
		if err != nil {
			log.Println("parsedirent err:", err)
			return names, size, dir, err
		}
	}
	return names, size, dir, nil
}

func dirSize(fp string) (rsize int64, fileErr error) {
	names, rsize, dir, fileErr := fileCheck(fp)
	if fileErr != nil {
		return rsize, fileErr
	}
	if !dir {
		return rsize, nil
	}
	for _, name := range names {
		tsize, fileErr := dirSize(path.Join(fp, name))
		if fileErr != nil {
			return rsize, fileErr
		}
		rsize += tsize
	}
	return rsize, fileErr
}
