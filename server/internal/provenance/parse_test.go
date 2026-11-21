package provenance

import (
	"strings"
	"testing"
)

// A ComfyUI graph: checkpoint -> LoRA -> KSampler, with the seed from a primitive node and the
// prompt behind a concatenation; a second, unused checkpoint loader that must not be chosen.
const comfyGraph = `{
 "1": {"class_type": "CheckpointLoaderSimple", "inputs": {"ckpt_name": "sdxl\\juggernautXL_v9.safetensors"}},
 "2": {"class_type": "LoraLoader", "inputs": {"model": ["1", 0], "clip": ["1", 1], "lora_name": "chars\\Ada_v2.safetensors", "strength_model": 0.8}},
 "3": {"class_type": "CLIPTextEncode", "inputs": {"clip": ["2", 1], "text": ["4", 0]}},
 "4": {"class_type": "StringConcatenate", "inputs": {"string_a": "a portrait of ada", "string_b": "golden hour", "delimiter": ", "}},
 "5": {"class_type": "CLIPTextEncode", "inputs": {"clip": ["2", 1], "text": "blurry, lowres"}},
 "6": {"class_type": "PrimitiveNode", "inputs": {"value": 123456789012345680000}},
 "7": {"class_type": "KSampler", "inputs": {"model": ["2", 0], "positive": ["3", 0], "negative": ["5", 0], "seed": ["6", 0],
       "steps": 30, "cfg": 5.5, "sampler_name": "dpmpp_2m", "scheduler": "karras", "denoise": 1, "latent_image": ["8", 0]}},
 "8": {"class_type": "EmptyLatentImage", "inputs": {"width": 832, "height": 1216, "batch_size": 1}},
 "9": {"class_type": "CheckpointLoaderSimple", "inputs": {"ckpt_name": "aaa_unused.safetensors"}},
 "10": {"class_type": "SaveImage", "inputs": {"images": ["7", 0]}}
}`

func TestComfyUI(t *testing.T) {
	g, nf := Parse(Tags{"PNG:Prompt": comfyGraph, "PNG:Workflow": `{"nodes": [{"id": 1, "type": "KSampler"}]}`}, false)
	if g == nil || nf {
		t.Fatalf("got %v, needsFile %v", g, nf)
	}
	if g.Source != "comfyui" || g.Kind != "generated" || g.Evidence != "png:prompt" || g.Tool != "ComfyUI" {
		t.Errorf("identity = %+v", g)
	}
	if g.Prompt != "a portrait of ada, golden hour" || g.Negative != "blurry, lowres" {
		t.Errorf("prompt %q negative %q", g.Prompt, g.Negative)
	}
	if g.Model != "juggernautXL_v9" {
		t.Errorf("model = %q, want the one feeding the sampler", g.Model)
	}
	if len(g.Loras) != 1 || g.Loras[0].Name != "ada_v2" || g.Loras[0].Display != "Ada_v2" || *g.Loras[0].Weight != 0.8 {
		t.Errorf("loras = %+v", g.Loras)
	}
	if g.Steps != 30 || g.CFG != 5.5 || g.Sampler != "dpmpp_2m" || g.Scheduler != "karras" || g.Width != 832 || g.Height != 1216 {
		t.Errorf("settings = %+v", g)
	}
	if g.Seed != "123456789012345680000" { // exactly as written, past what a float64 holds
		t.Errorf("seed = %q", g.Seed)
	}
	if g.Graph == "" || g.Workflow == "" {
		t.Error("the graph and workflow should be kept as found")
	}
}

func TestComfyUINotGenerative(t *testing.T) {
	// An upscale-only graph: ComfyUI touched the file, but didn't generate it.
	up := `{"1": {"class_type": "LoadImage", "inputs": {"image": "photo.png"}},
	        "2": {"class_type": "UpscaleModelLoader", "inputs": {"model_name": "4x-UltraSharp.pth"}},
	        "3": {"class_type": "ImageUpscaleWithModel", "inputs": {"upscale_model": ["2", 0], "image": ["1", 0]}},
	        "4": {"class_type": "SaveImage", "inputs": {"images": ["3", 0]}}}`
	if g, _ := Parse(Tags{"PNG:Prompt": up}, false); g != nil {
		t.Errorf("an upscale counted as generated: %s", g.Summary())
	}
}

func TestComfyUIVideoComment(t *testing.T) {
	// VideoHelperSuite: one JSON comment holding the graph as a string, with Python's NaN.
	graph := strings.ReplaceAll(comfyGraph, `"denoise": 1`, `"denoise": NaN`)
	comment := `{"prompt": ` + quote(graph) + `, "workflow": "{\"nodes\": []}"}`
	g, _ := Parse(Tags{"ItemList:Comment": comment}, true)
	if g == nil || g.Evidence != "video:comment" || g.Prompt != "a portrait of ada, golden hour" {
		t.Fatalf("got %s", g.Summary())
	}
}

func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return `"` + r.Replace(s) + `"`
}

func TestCutShortInTheIndex(t *testing.T) {
	cutValue := map[string]any{"truncated": true, "length": 40000.0}
	g, nf := Parse(Tags{"PNG:Prompt": cutValue}, false)
	if g == nil || !g.NeedsFile || !nf || g.Source != "comfyui" {
		t.Errorf("a ComfyUI graph too big for the index: %s, needsFile %v", g.Summary(), nf)
	}
	// A long comment might be anything: no label yet, but read the file.
	g, nf = Parse(Tags{"ItemList:Comment": cutValue}, true)
	if g != nil || !nf {
		t.Errorf("a long comment: %s, needsFile %v", g.Summary(), nf)
	}
}

func TestA1111(t *testing.T) {
	params := "a red fox in snow <lora:winterFX:0.6>, cinematic\nNegative prompt: cartoon, text\n" +
		`Steps: 28, Sampler: DPM++ 2M, Schedule type: Karras, CFG scale: 6.5, Seed: 42, Size: 1024x1536, Model hash: abc, Model: realvisXL_v40, Lora hashes: "winterFX: 1a2b, other: 3c4d", Version: f2.0.1v1.10.1`
	g, _ := Parse(Tags{"PNG:Parameters": params}, false)
	if g == nil || g.Source != "a1111" || g.Tool != "Forge" || g.ToolVersion != "f2.0.1v1.10.1" {
		t.Fatalf("got %s", g.Summary())
	}
	if g.Prompt != "a red fox in snow , cinematic" || g.Negative != "cartoon, text" {
		t.Errorf("prompt %q negative %q", g.Prompt, g.Negative)
	}
	if g.Steps != 28 || g.Sampler != "DPM++ 2M" || g.Scheduler != "Karras" || g.CFG != 6.5 || g.Seed != "42" || g.Width != 1024 || g.Height != 1536 || g.Model != "realvisXL_v40" {
		t.Errorf("settings %+v", g)
	}
	if len(g.Loras) != 2 || g.Loras[1].Name != "winterfx" || g.Loras[1].Weight == nil || *g.Loras[1].Weight != 0.6 {
		t.Errorf("loras %+v", g.Loras)
	}
	// The same text in a JPEG's EXIF user comment.
	if g, _ := Parse(Tags{"ExifIFD:UserComment": params}, false); g == nil || g.Evidence != "exif:usercomment" {
		t.Errorf("user comment: %s", g.Summary())
	}
}

func TestOtherEvidence(t *testing.T) {
	sig := "Signature: " + strings.Repeat("QUJD", 40)
	cases := []struct {
		name   string
		tags   Tags
		video  bool
		source string
		tool   string
		kind   string
	}{
		{"midjourney", Tags{"PNG:Description": "a cat astronaut --ar 2:3 --v 6.1 Job ID: 3f862298-412c-4910-877b-a86e796f0d65"}, false, "midjourney", "Midjourney", "generated"},
		{"grok image", Tags{"IFD0:ImageDescription": sig, "ExifIFD:UserComment": sig}, false, "grok", "Grok Imagine", "generated"},
		{"grok video", Tags{"ItemList:Comment": sig}, true, "grok", "Grok Imagine", "generated"},
		{"kling", Tags{"PNG:AIGC": `{"Label":"1","ContentProducer":"001191110108335469089C10100","ProduceID":"KLingMuse_x"}`}, false, "aigc", "Kling", "generated"},
		{"c2pa openai", Tags{"CBOR:ActionsDigitalSourceType": "http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia",
			"CBOR:Claim_Generator_InfoName": "OpenAI Media Service API", "CBOR:ActionsSoftwareAgentName": "gpt-image"}, false, "c2pa", "OpenAI", "generated"},
		{"c2pa google edit", Tags{"CBOR:ActionsDigitalSourceType": "http://cv.iptc.org/newscodes/digitalsourcetype/compositeWithTrainedAlgorithmicMedia",
			"CBOR:Claim_Generator_InfoName": "Google C2PA Core Generator Library", "CBOR:Description": "Input ingredient 0"}, false, "c2pa", "Google AI", "edited"},
		{"iptc", Tags{"XMP-iptcExt:DigitalSourceType": "http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia"}, false, "iptc", "", "generated"},
		{"google credit", Tags{"XMP-photoshop:Credit": "Made with Google AI"}, false, "iptc", "Google AI", "generated"},
	}
	for _, c := range cases {
		g, _ := Parse(c.tags, c.video)
		if g == nil || g.Source != c.source || g.Tool != c.tool || g.Kind != c.kind {
			t.Errorf("%s: got %s", c.name, g.Summary())
		}
	}
	if g, _ := Parse(Tags{"CBOR:ActionsDigitalSourceType": "http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia",
		"CBOR:Claim_Generator_InfoName": "OpenAI Media Service API", "CBOR:ActionsSoftwareAgentName": "gpt-image"}, false); g.Model != "gpt-image" {
		t.Errorf("c2pa model = %q", g.Model)
	}
	// The C2PA ingredient label must never read as a prompt.
	if g, _ := Parse(Tags{"CBOR:ActionsDigitalSourceType": "trainedAlgorithmicMedia", "CBOR:Description": "Input ingredient 0"}, false); g.Prompt != "" {
		t.Errorf("prompt from an ingredient label: %q", g.Prompt)
	}
}

func TestNoEvidence(t *testing.T) {
	for _, tags := range []Tags{
		{"IFD0:Make": "Apple", "IFD0:Model": "iPhone 15 Pro", "IFD0:Software": "17.4"},
		{"IFD0:Software": "Topaz Photo AI 3.4.4 (Windows)"}, // an AI enhancer on a real photo isn't a generation
		{"IFD0:ImageDescription": "Sunset at the beach with the kids"},
		{"File:Comment": "Lavc59.37.100"},
		{"PNG:Description": "a photo --ar 2:3"}, // Midjourney-like text without a job id
	} {
		if g, nf := Parse(tags, false); g != nil || nf {
			t.Errorf("%v: got %s, needsFile %v", tags, g.Summary(), nf)
		}
	}
}

func TestRefs(t *testing.T) {
	// Image-to-video: the start image feeds the sampler through an encoder; a LoadImage left
	// unwired doesn't count, nor does the output's file name prefix.
	g := `{
	 "1": {"class_type": "LoadImage", "inputs": {"image": "ada_portrait_00012_.png [output]"}},
	 "2": {"class_type": "WanImageToVideo", "inputs": {"start_image": ["1", 0], "positive": ["4", 0]}},
	 "3": {"class_type": "KSampler", "inputs": {"positive": ["2", 0], "model": ["5", 0]}},
	 "4": {"class_type": "CLIPTextEncode", "inputs": {"text": "she turns"}},
	 "5": {"class_type": "UNETLoader", "inputs": {"unet_name": "wan2.2_i2v_14B.safetensors"}},
	 "6": {"class_type": "LoadImage", "inputs": {"image": "unused.png"}},
	 "7": {"class_type": "VHS_VideoCombine", "inputs": {"images": ["3", 0], "filename_prefix": "clips/ada.png"}}
	}`
	if got := Refs(g, ""); len(got) != 1 || got[0] != "ada_portrait_00012_.png" {
		t.Errorf("refs = %v", got)
	}
	if !UsesStartImage("wan2.2_i2v_14B") || UsesStartImage("Wan2.2-T2V-A14B-HighNoise-Q8_0") || !UsesStartImage("minimax_h3_ref2va") {
		t.Error("start-image models")
	}
	// The editor workflow, when there's no API graph.
	wf := `{"nodes": [{"id": 1, "type": "LoadImage", "widgets_values": ["C:\\ComfyUI\\input\\emma_ref.jpg", "image"]},
	                  {"id": 2, "type": "KSampler", "widgets_values": [1, "fixed"]},
	                  {"id": 3, "type": "LoadImage", "mode": 4, "widgets_values": ["bypassed.png"]}],
	        "links": [[1, 1, 0, 2, 0, "IMAGE"], [2, 3, 0, 2, 1, "IMAGE"]]}`
	if got := Refs("", wf); len(got) != 1 || got[0] != "emma_ref.jpg" {
		t.Errorf("workflow refs = %v", got)
	}
}
