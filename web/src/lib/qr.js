import QRCode from 'qrcode'

// Drawing a tunnel profile as something a camera can read.
//
// A profile is around 350 bytes, and an obfuscated one 500, which puts the code
// at 65 to 90 modules a side. At that density the size cannot be chosen by the
// layout: it has to come from the code itself, so that every module lands on a
// whole number of pixels and the browser never scales the image. A code drawn
// at three and a half pixels per module is one a phone gives up on.
//
// It lives here rather than in a view because there are two places that show a
// profile, and when this was written out twice only one of them was ever fixed.

// The specification asks for four modules of white around the code, and a
// scanner uses that border to find the code at all. One is not a quiet zone,
// it is a hairline.
const quietZone = 4

// The lowest correction level, deliberately. Every level above it adds modules
// to a code that is already dense, and there is nothing on a screen to correct
// for. Fewer, larger modules is the whole trade.
const correction = 'L'

// What the code should come out near. The real size is rounded from this to
// keep the modules on whole pixels, so it is a target rather than a width.
const targetPx = 340

// makeQR returns the image and the size it must be shown at.
//
// Both are needed together: showing the image at any other size reintroduces
// the scaling this exists to avoid.
export async function makeQR(text) {
  const shape = QRCode.create(text, { errorCorrectionLevel: correction })
  const across = shape.modules.size + quietZone * 2
  const scale = Math.max(3, Math.round(targetPx / across))

  return {
    dataUrl: await QRCode.toDataURL(text, {
      margin: quietZone,
      errorCorrectionLevel: correction,
      scale,
      // Fixed rather than themed. A dark code on a dark background is not a
      // code, and the panel has a dark theme.
      color: { dark: '#000000', light: '#ffffff' },
    }),
    size: across * scale,
  }
}
