# Hongen doorbase latest hardware baseline

This is the single current purchasing baseline for the schematic. Older
schematic assumptions about bare CC1101 silicon, a discrete balun, or a bare
audio amplifier are obsolete.

| Function | Purchased item | Taobao item | Schematic treatment |
|---|---|---|---|
| CC1101 RF | AS07-M1101S, IPEX version | https://item.taobao.com/item.htm?id=36605719280 | 1.25 mm module header; pinout must follow the purchased board |
| SD storage | Mini SD/TF adapter module | https://item.taobao.com/item.htm?id=708575880467 | 1.25 mm module header; SPI labels |
| Talk audio | ES8311 + NS4150B audio module, onboard microphone/amplifier | https://item.taobao.com/item.htm?id=940967898928 | 1.25 mm module header; I2S/audio/power labels |
| Optional microphone reference | INMP441/MSM3526 I2S module | https://item.taobao.com/item.htm?id=930040367194 | Use only if the purchased audio module does not include the required mic |
| Optional amplifier reference | MAX98357 I2S amplifier module | https://item.taobao.com/item.htm?id=954709712228 | Use only when the audio-module variant is not used |
| ESP32 | ESP32-S3-WROOM-1U-N8R8 | https://item.taobao.com/item.htm?id=701699681029 | Bare module plus local power/reset/boot circuitry |

All module interfaces use 1.25 mm pitch unless the purchased item is
explicitly documented otherwise. Pin numbers are intentionally not guessed;
they are to be bound after the product-page pinout or a photo of the actual
board is available.
