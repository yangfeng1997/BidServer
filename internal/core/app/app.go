package app

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"

	"project/pkg/logger"
)

type App interface {
	Start() error
	Shutdown()
	Reload() error
	GetDieChan() chan bool
	GetNodeId() string
	IsRunning() bool
	RegisterModuleBefore(module Module, name string) error
	RegisterModule(module Module, name string) error
	RegisterModuleAfter(module Module, name string) error
	GetModule(name string) (Module, error)
}

type BaseApp struct {
	dieChan         chan bool
	externalDieChan chan bool
	sgChan          chan os.Signal

	mu           sync.RWMutex
	running      bool
	shuttingDown bool
	startAt      time.Time
	modulesMap   map[string]Module
	modulesArray []moduleWrapper
}

func NewBaseApp(dieChan chan bool) *BaseApp {
	if dieChan == nil {
		dieChan = make(chan bool)
	}

	return &BaseApp{
		dieChan:         dieChan,
		externalDieChan: make(chan bool, 1),
		sgChan:          make(chan os.Signal, 1),
		startAt:         time.Now(),
		modulesMap:      make(map[string]Module),
	}
}

func (app *BaseApp) Start() error {
	app.mu.Lock()
	if app.running {
		app.mu.Unlock()
		return errors.New("app already running")
	}
	app.running = true
	app.mu.Unlock()

	started := false
	defer func() {
		if !started {
			app.Shutdown()
		}
	}()

	for _, wrapper := range app.modulesArray {
		if err := wrapper.module.Init(app); err != nil {
			return fmt.Errorf("init module %s: %w", wrapper.name, err)
		}
	}

	for _, wrapper := range app.modulesArray {
		if err := wrapper.module.AfterInit(); err != nil {
			return fmt.Errorf("after init module %s: %w", wrapper.name, err)
		}
	}

	started = true
	signal.Notify(app.sgChan, appSignals()...)
	defer signal.Stop(app.sgChan)

	for {
		select {
		case <-app.dieChan:
			app.Shutdown()
			return nil
		case <-app.externalDieChan:
			app.Shutdown()
			return nil
		case sig := <-app.sgChan:
			if isReloadSignal(sig) {
				if err := app.Reload(); err != nil {
					logger.Error("reload app failed", logger.Err(err))
				} else {
					logger.Info("reload app ok")
				}
				continue
			}
			app.Shutdown()
			return nil
		}
	}
}

func (app *BaseApp) Shutdown() {
	app.mu.Lock()
	if app.shuttingDown {
		app.mu.Unlock()
		return
	}
	app.shuttingDown = true
	wasRunning := app.running
	app.running = false
	app.mu.Unlock()

	if !wasRunning {
		return
	}

	for i := len(app.modulesArray) - 1; i >= 0; i-- {
		app.modulesArray[i].module.BeforeStop()
	}

	for i := len(app.modulesArray) - 1; i >= 0; i-- {
		app.modulesArray[i].module.Stop()
	}
}

func (app *BaseApp) Reload() error {
	app.mu.RLock()
	if app.shuttingDown {
		app.mu.RUnlock()
		return errors.New("app is shutting down")
	}
	modules := append([]moduleWrapper(nil), app.modulesArray...)
	app.mu.RUnlock()

	for _, wrapper := range modules {
		reloadable, ok := wrapper.module.(Reloadable)
		if !ok {
			continue
		}
		if err := reloadable.Reload(); err != nil {
			return fmt.Errorf("reload module %s: %w", wrapper.name, err)
		}
	}
	return nil
}

func (app *BaseApp) GetDieChan() chan bool {
	return app.externalDieChan
}

func (app *BaseApp) GetNodeId() string {
	return ""
}

func (app *BaseApp) IsRunning() bool {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.running
}

func (app *BaseApp) RegisterModule(module Module, name string) error {
	return app.RegisterModuleAfter(module, name)
}

func (app *BaseApp) RegisterModuleAfter(module Module, name string) error {
	if err := app.validateModule(module, name); err != nil {
		return err
	}

	app.modulesMap[name] = module
	app.modulesArray = append(app.modulesArray, moduleWrapper{
		module: module,
		name:   name,
	})
	return nil
}

func (app *BaseApp) RegisterModuleBefore(module Module, name string) error {
	if err := app.validateModule(module, name); err != nil {
		return err
	}

	app.modulesMap[name] = module
	app.modulesArray = append([]moduleWrapper{{
		module: module,
		name:   name,
	}}, app.modulesArray...)

	return nil
}

func (app *BaseApp) GetModule(name string) (Module, error) {
	if module, ok := app.modulesMap[name]; ok {
		return module, nil
	}
	return nil, fmt.Errorf("module %s not found", name)
}

func (app *BaseApp) validateModule(module Module, name string) error {
	if name == "" {
		return errors.New("module name is empty")
	}
	if module == nil {
		return fmt.Errorf("module %s is nil", name)
	}
	if _, ok := app.modulesMap[name]; ok {
		return fmt.Errorf("module %s already registered", name)
	}
	return nil
}
