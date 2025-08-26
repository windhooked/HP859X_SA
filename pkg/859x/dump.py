import pyvisa
import struct

# Initialize VISA connection
rm = pyvisa.ResourceManager()
inst = rm.open_resource('USB0::0x03EB::0x2065::HP8563E_007::INSTR')
inst.timeout = 5000  # Set timeout to 5 seconds

OUTPUT_FILE = "dump.bin"

with open(OUTPUT_FILE, "wb") as f:
    for addr in range(0x000000, 0x1000000):  # Iterate from 0 to 0xFFFFFF
        addr_hex = f"{addr:06X}"  # Format address as 6-digit uppercase hex
        inst.write(f"ZSETADDR {addr_hex}")  # Set address
        byte_str = inst.query('ZRDWR?').strip()  # Query byte data
        value = int(byte_str)  # Convert to integer
        f.write(struct.pack('B', value))  # Write byte to file
        if addr % 0x100 == 0:  # Print progress every 256 bytes
            print(f"Address {addr_hex}: Value={value:02X}")

print(f"Memory dump complete. File saved as {OUTPUT_FILE}")
