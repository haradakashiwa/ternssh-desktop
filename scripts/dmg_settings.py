# The disk image of the macOS app, for dmgbuild: the app on the left, a link
# to /Applications on the right, over resources/dmg-background.png (and its
# @2x, which dmgbuild joins to it). scripts/dmg.sh gives the defines:
#
#   app   the .app bundle
#   root  the repository
import os

app = defines["app"]  # noqa: F821, given by dmgbuild
root = defines["root"]  # noqa: F821
name = os.path.basename(app)

format = "ULMO"
filesystem = "HFS+"
files = [app]
symlinks = {"Applications": "/Applications"}
icon = os.path.join(app, "Contents", "Resources", "AppIcon.icns")

background = os.path.join(root, "resources", "dmg-background.png")
window_rect = ((200, 120), (660, 428))
default_view = "icon-view"
show_status_bar = False
show_tab_view = False
show_toolbar = False
show_pathbar = False
show_sidebar = False
show_icon_preview = False
include_icon_view_settings = True
arrange_by = None
icon_size = 128
text_size = 14
icon_locations = {
    name: (170, 180),
    "Applications": (490, 180),
}
