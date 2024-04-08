package main

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"syscall"
)

// Usage: your_docker.sh run <image> <command> <arg1> <arg2> ...
func main() {
	command := os.Args[3]
	args := os.Args[4:len(os.Args)]

	chroot := path.Join(os.TempDir(), fmt.Sprintf("%d", os.Getpid()))
	chrootedCommand := path.Join(chroot, command)
	err := os.MkdirAll(chroot, 0755)
	if err != nil {
		fmt.Fprintf(os.Stderr, "temp dir err: %v", err)
		os.Exit(1)
	}
	commandFileData, err := os.ReadFile(command)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command read err: %v", err)
		os.Exit(1)
	}
	err = os.MkdirAll(path.Dir(chrootedCommand), 0755)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command dir create err: %v", err)
		os.Exit(1)
	}
	file, err := os.Create(chrootedCommand)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command file create err: %v", err)
		os.Exit(1)
	}
	os.Chmod(chrootedCommand, 0777)
	_, err = file.Write(commandFileData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "command write err: %v", err)
		os.Exit(1)
	}
	file.Close()

	cmd := exec.Command(command, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Chroot: chroot,
	}
	err = cmd.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "err: %v", err)
		os.Exit(cmd.ProcessState.ExitCode())
	}
}
