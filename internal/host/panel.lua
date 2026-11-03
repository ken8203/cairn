-- Toggles a floating layer running args.command over the window holding args.block,
-- or with args.close only closes it.
local args = rex.args
local view, err = rex.session.view{}
if not view then error(err) end

local window
for _, w in ipairs(view.windows) do
  for _, layer in ipairs(w.layers) do
    for _, block in ipairs(layer.blocks) do
      if block.block_id == args.block then window = w end
    end
  end
end
if not window then error("no window holds " .. args.block) end

for _, layer in ipairs(window.layers) do
  if layer.kind ~= "tiled" then
    for _, block in ipairs(layer.blocks) do
      if block.label == args.label then
        rex.session.close_layer{ layer_id = layer.layer_id }
        return "closed"
      end
    end
  end
end
if args.close then return "closed" end

rex.session.new_layer{
  window_id = window.window_id,
  bounds = { 0.1, 0.1, 0.8, 0.5 },
  focus = true,
  layout = { block = { flavor = "com.superlogical.terminal.shell", label = args.label,
    options = { command = args.command } } },
}
return "opened"
