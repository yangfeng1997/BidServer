package app

type Builder interface {
	Build() (App, error)
	AddModule(name string, module Module)
	AddPostBuildHook(hook func(App) error)
}

type BaseBuilder struct {
	dieChan             chan bool
	moduleRegistrations []ModuleRegistration
	postBuildHooks      []func(App) error
}

func NewBaseBuilder(dieChan chan bool) *BaseBuilder {
	if dieChan == nil {
		dieChan = make(chan bool)
	}

	return &BaseBuilder{
		dieChan: dieChan,
	}
}

func (builder *BaseBuilder) AddModule(name string, module Module) {
	builder.moduleRegistrations = append(builder.moduleRegistrations, ModuleRegistration{
		Name:   name,
		Module: module,
	})
}

func (builder *BaseBuilder) AddPostBuildHook(hook func(App) error) {
	builder.postBuildHooks = append(builder.postBuildHooks, hook)
}

func (builder *BaseBuilder) Build() (App, error) {
	app := NewBaseApp(builder.dieChan)

	for _, registration := range builder.moduleRegistrations {
		if err := app.RegisterModule(registration.Module, registration.Name); err != nil {
			return nil, err
		}
	}

	for _, postBuildHook := range builder.postBuildHooks {
		if err := postBuildHook(app); err != nil {
			return nil, err
		}
	}

	return app, nil
}
