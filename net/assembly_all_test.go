package net

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func init() {
	runtime.GOMAXPROCS(runtime.NumCPU())
}

func TestLoadAllAssemblies(t *testing.T) {
	var paths []string
	if mp := MonoDefaultPath(); mp != "" {
		paths = append(paths, mp)
	}
	if p2, err := WindowsFrameworks(); err == nil {
		paths = append(paths, p2...)
	}
	if len(paths) == 0 {
		t.Skip("Neither mono nor Windows .NET Framework paths were possible to get")
	}
	var (
		inChan  = make(chan string, 64)
		outChan = make(chan error, 32)
		wg      sync.WaitGroup
		once    sync.Once
	)
	worker := func() {
		wg.Add(1)

		for fn := range inChan {
			data, err := ioutil.ReadFile(fn)
			if err != nil {
				outChan <- errors.New(fmt.Sprintf("%s: %s\n", fn, err))
				continue
			}
			if asm, err := LoadAssembly(bytes.NewReader(data)); err != nil {
				if err != ErrNotAssembly && err != ErrMetadata {
					outChan <- errors.New(fmt.Sprintf("%s: %s\n", fn, err))
				} else {
					t.Logf("%s is not a .net assembly", fn)
				}
			} else {
				ci := ConcreteTableIndex{metadataUtil: &asm.MetadataUtil, table: id_TypeDef, index: 1}
				for i := uint32(0); i < asm.Tables[id_TypeDef].Rows; i++ {
					ci.index = i + 1
					if _, err := asm.Fields(&ci); err != nil {
						outChan <- errors.New(fmt.Sprintf("%s: %s\n", fn, err))
					}
					if _, err := asm.Methods(&ci); err != nil {
						outChan <- errors.New(fmt.Sprintf("%s: %s\n", fn, err))
					}
					if raw, err := ci.Data(); err != nil {
						outChan <- errors.New(fmt.Sprintf("%s: %s\n", fn, err))
					} else {
						c := raw.(*TypeDefRow)
						if c.Flags&TypeAttributes_Interface == 0 {
							if _, err := asm.Extends(&ci); err != nil {
								outChan <- errors.New(fmt.Sprintf("%s: %s\n", fn, err))
							}
						}
					}
					if _, err := asm.Implements(&ci); err != nil {
						outChan <- errors.New(fmt.Sprintf("%s: %s\n", fn, err))
					}
				}
				ci.table = id_Module
				ci.index = 1
				if d, err := ci.Data(); err != nil {
					outChan <- errors.New(fmt.Sprintf("%s: %s\n", fn, err))
				} else {
					mr := d.(*ModuleRow)
					ci.table = id_Assembly
					if d2, err := ci.Data(); err != nil {
						if asm.Tables[id_Assembly].Ptr == 0 {
							// It's ok for an assembly to not have an assembly table, although
							// I've only ever seen this with System.EnterpriseServices.Wrapper.dll
							t.Logf("Successfully loaded module %50s {%s}", mr.Name, mr.Mvid)
						} else {
							outChan <- errors.New(fmt.Sprintf("%s: %s\n", fn, err))
						}
					} else {
						ar := d2.(*AssemblyRow)
						if mn, an := string(mr.Name), string(ar.Name); !strings.HasPrefix(mn, an) && (an != "mscorlib" && mn != "CommonLanguageRuntimeLibrary") {
							outChan <- errors.New(fmt.Sprintf("The assembly name isn't the prefix of the module name: %s, %s", an, mn))
						} else {
							t.Logf("Successfully loaded module %50s {%s} %s", mn, mr.Mvid, an)
						}
					}
				}
			}
		}
		wg.Done()
		wg.Wait()
		once.Do(func() {
			close(outChan)
		})
	}
	for i := 0; i < runtime.NumCPU(); i++ {
		go worker()
	}

	for _, path := range paths {
		if f, err := os.Open(path); err != nil {
			t.Error(err)
		} else {
			fi, err := f.Readdir(0)
			if err != nil {
				t.Fatal(err)
			}

			for i := 0; i < len(fi); {
				if fn := fi[i].Name(); strings.HasSuffix(fn, ".dll") {
					if len(inChan)+1 >= cap(inChan) {
						for len(outChan) > 0 {
							o := <-outChan
							if o != nil {
								t.Error(o)
							}
						}
						time.Sleep(time.Microsecond * 100)
					} else {
						inChan <- path + fn
						i++
					}
				} else {
					i++
				}
			}
		}
	}
	close(inChan)
	for o := range outChan {
		if o != nil {
			t.Error(o)
		}
	}
}
