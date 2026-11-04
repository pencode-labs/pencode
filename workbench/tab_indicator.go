package workbench

import htmlui "github.com/diiyw/onix/ui/html"

var tabBusyIndicator = [...]htmlui.VNode{{
	Key: 2, Tag: "span", Attributes: []htmlui.VNodeAttribute{{Name: "class", Value: "pencode-tab-busy-icon"}, {Name: "aria-hidden", Value: "true"}},
	Children: []htmlui.VNode{{Key: 0, Tag: "span"}, {Key: 1, Tag: "span"}, {Key: 2, Tag: "span"}, {Key: 3, Tag: "span"}, {Key: 4, Tag: "span"}, {Key: 5, Tag: "span"}, {Key: 6, Tag: "span"}, {Key: 7, Tag: "span"}},
}}

var tabUnreadIndicator = [...]htmlui.VNode{{Key: 1, Tag: "span", Attributes: []htmlui.VNodeAttribute{{Name: "class", Value: "pencode-tab-unread-icon"}, {Name: "aria-hidden", Value: "true"}}}}

// Reconcile the active indicator as a child. Toggling display on retained
// animated boxes forces the renderer to rebuild the whole surface.
func renderTabIndicator(view *htmlui.View, props *tabProps, foreground string) {
	switch {
	case props.Busy:
		view.Keyed("close", tabBusyIndicator[:])
	case props.Unread:
		view.Keyed("close", tabUnreadIndicator[:])
	default:
		nodes := [...]htmlui.VNode{{Key: 0, Tag: "img", Attributes: []htmlui.VNodeAttribute{{Name: "class", Value: "pencode-close-icon"}, {Name: "src", Value: props.Model.themedIcon("icons/close.svg", foreground)}, {Name: "alt", Value: ""}}}}
		view.Keyed("close", nodes[:])
	}
}
