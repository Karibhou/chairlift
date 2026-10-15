// Package ui loads ChairLift's Blueprint-described widget trees.
//
// Each internal/ui/<name>.blp is compiled by scripts/blueprints.sh (`make
// blueprints`) into the GtkBuilder XML <name>.ui beside it, and both are
// committed: the Go build stays pure and embeds the XML, while CI recompiles
// every blueprint in the pinned Dakota image and fails when a committed .ui
// is stale. The .blp is the source; never edit a .ui by hand.
//
// puregotk cannot subclass a widget from a GtkBuilder template, and GtkBuilder
// cannot resolve Go signal handlers by name, so a blueprint here describes a
// plain object tree with ids and no signal handlers. Go code binds the
// objects it needs with Bind into a struct whose fields carry `ui:"<id>"`
// tags, plus one blank File field naming the blueprint:
//
//	type windowUI struct {
//		_         ui.File                   `ui:"window"`
//		SplitView *adw.NavigationSplitView `ui:"split_view"`
//	}
//
// and then connects signals in Go, once, at build time. That shape is what
// internal/installcheck's TestBlueprintBindingsMatchTheirUI reads without
// GTK: every tagged field must name an object of the matching class in the
// blueprint, so a renamed id or a changed widget type fails `make ci` instead
// of panicking at startup.
package ui

import (
	"embed"
	"fmt"
	"reflect"

	"codeberg.org/puregotk/puregotk/v4/adw"
	"codeberg.org/puregotk/puregotk/v4/gobject"
	"codeberg.org/puregotk/puregotk/v4/gtk"
)

//go:embed *.ui
var files embed.FS

// File marks the blank field that names a binding struct's blueprint.
type File struct{}

var fileType = reflect.TypeOf(File{})

// Bind builds a fresh copy of the blueprint named by target's File field and
// stores each tagged object in its field. target must be a pointer to a
// struct. Every object is constructed anew, so one blueprint may be bound
// many times (one per page, say). Bind panics on a missing blueprint, id, or
// malformed tag: those are programming errors in compiled-in data, and
// TestBlueprintBindingsMatchTheirUI catches them before a binary is built.
//
// Bind must run on the GTK main thread, after the libadwaita types are
// registered (adw.Init or AdwApplication startup).
func Bind(target any) *gtk.Builder {
	value := reflect.ValueOf(target)
	if value.Kind() != reflect.Pointer || value.Elem().Kind() != reflect.Struct {
		panic(fmt.Sprintf("ui.Bind: want a pointer to a struct, got %T", target))
	}
	value = value.Elem()
	kind := value.Type()

	name := ""
	for i := 0; i < kind.NumField(); i++ {
		if kind.Field(i).Type == fileType {
			name = kind.Field(i).Tag.Get("ui")
		}
	}
	if name == "" {
		panic(fmt.Sprintf("ui.Bind: %s has no ui.File field naming its blueprint", kind))
	}
	data, err := files.ReadFile(name + ".ui")
	if err != nil {
		panic(fmt.Sprintf("ui.Bind: no blueprint %q: %v", name, err))
	}
	builder := gtk.NewBuilderFromString(string(data), len(data))
	if builder == nil {
		panic(fmt.Sprintf("ui.Bind: blueprint %q did not build", name))
	}

	for i := 0; i < kind.NumField(); i++ {
		field := kind.Field(i)
		id := field.Tag.Get("ui")
		if id == "" || field.Type == fileType {
			continue
		}
		if field.Type.Kind() != reflect.Pointer {
			panic(fmt.Sprintf("ui.Bind: %s.%s must be a pointer", kind, field.Name))
		}
		object := builder.GetObject(id)
		if object == nil {
			panic(fmt.Sprintf("ui.Bind: blueprint %q has no object %q", name, id))
		}
		bound := reflect.New(field.Type.Elem())
		ptr, ok := bound.Interface().(gobject.Ptr)
		if !ok {
			panic(fmt.Sprintf("ui.Bind: %s.%s is not a GObject wrapper", kind, field.Name))
		}
		ptr.SetGoPointer(object.GoPointer())
		value.Field(i).Set(bound)
	}
	return builder
}

// SupportsShortcutsDialog reports whether the running libadwaita has
// AdwShortcutsDialog (1.8). GtkBuilder aborts the process on a class it does
// not know, so the shortcuts blueprint is bound only when this holds; the
// window falls back to shortcuts-legacy otherwise. Every other blueprint
// uses classes from libadwaita 1.6 or earlier, the floor ChairLift supports
// (TestBlueprintsStayWithinTheLibadwaitaFloor).
func SupportsShortcutsDialog() bool {
	major, minor := adw.GetMajorVersion(), adw.GetMinorVersion()
	return major > 1 || (major == 1 && minor >= 8)
}
