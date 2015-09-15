package main

import (
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	"github.com/MStoykov/mp4"
	"github.com/MStoykov/mp4/filter"
	cli "github.com/jawher/mow.cli"
)

func main() {
	go func() {
		fmt.Println(http.ListenAndServe(":6060", nil))
	}()

	cmd := cli.App("mp4tool", "MP4 command line tool")

	cmd.Command("info", "Displays information about a media", func(cmd *cli.Cmd) {
		file := cmd.StringArg("FILE", "", "the file to display")
		cmd.Action = func() {
			fd, err := os.Open(*file)
			defer fd.Close()
			v, err := mp4.Decode(fd)
			if err != nil {
				fmt.Println(err)
			}
			v.Dump()
		}
	})

	cmd.Command("clip", "Generates a clip", func(cmd *cli.Cmd) {
		start := cmd.IntOpt("s start", 0, "start time (sec)")
		duration := cmd.IntOpt("d duration", 10, "duration (sec)")
		src := cmd.StringArg("SRC", "", "the source file name")
		dst := cmd.StringArg("DST", "", "the destination file name")
		cmd.Action = func() {
			in, err := os.Open(*src)
			if err != nil {
				fmt.Println(err)
				return
			}
			defer in.Close()
			v, err := mp4.Decode(in)
			if err != nil {
				fmt.Println(err)
				return
			}
			out, err := os.Create(*dst)
			if err != nil {
				fmt.Println(err)
				return
			}
			defer out.Close()
			clip, err := filter.Clip(v, time.Duration(*start)*time.Second, time.Duration(*duration)*time.Second)
			if err != nil {
				fmt.Println(err)
				return
			}
			if err := clip.Filter(); err != nil {
				fmt.Println(err)
				return
			}
			size, err := clip.WriteTo(out)
			if err != nil {
				fmt.Println(err)
			}
			fmt.Println("wrote", size)
		}
	})

	cmd.Command("copy", "Decodes a media and reencodes it to another file", func(cmd *cli.Cmd) {
		src := cmd.StringArg("SRC", "", "the source file name")
		dst := cmd.StringArg("DST", "", "the destination file name")
		cmd.Action = func() {
			in, err := os.Open(*src)
			if err != nil {
				fmt.Println(err)
			}
			defer in.Close()
			v, err := mp4.Decode(in)
			if err != nil {
				fmt.Println(err)
			}
			out, err := os.Create(*dst)
			if err != nil {
				fmt.Println(err)
			}
			defer out.Close()
			v.Encode(out)
		}
	})
	cmd.Run(os.Args)
	fmt.Println("press return to exit the program")
	fmt.Scanln()
}
