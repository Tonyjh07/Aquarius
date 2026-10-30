package characterfs

import (
	"bytes"
	"fmt"
	"image"
	"image/png"

	// 注册解码器：图标资产可能是 webp（`.charx` 实测多见），也可能是 jpeg / gif。
	_ "golang.org/x/image/webp"
	_ "image/gif"
	_ "image/jpeg"
)

// encodeAvatar 把头像来源重编码为只含像素的 PNG（§5.17）。
//
// PNG 卡把整份卡 JSON（有的还两份）塞在 `tEXt` 块里——实测一张 52 MB 的卡，像素只占
// 3 MB，其余全是 tEXt。直接把原图当头像等于把卡数据再存一份；重编码后只留像素，
// `card.json` 才是卡的事实来源。像素无损（decoded → PNG），带透明与调色板都保留。
func encodeAvatar(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return nil, nil
	}
	img, _, err := image.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("头像不是可识别的图片: %w", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("头像重编码失败: %w", err)
	}
	return buf.Bytes(), nil
}
