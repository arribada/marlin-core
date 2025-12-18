#!/usr/bin/env python3
import os
import sys
import serial
import time
import subprocess
import random


try:
    from gen import core_pb2
except ImportError:
    print("core_pb2 module not found. Attempting to compile from .proto file...")

    # Verify APP_ROOT environment variable
    if 'APP_ROOT' not in os.environ:
        print("Error: APP_ROOT environment variable is not set")
        print("Set it to your application root directory (e.g., export APP_ROOT=/path/to/app)")
        sys.exit(1)

    generated_dir = os.path.join(os.environ['APP_ROOT'], 'scripts', 'gen')
    os.makedirs(generated_dir , exist_ok=True)

    # Construct proper .proto file path
    proto_file = os.path.join(os.environ['APP_ROOT'], 'proto', 'core.proto')

    # Verify .proto file exists
    if not os.path.exists(proto_file):
        print(f"Error: core.proto not found at {proto_file}")
        print("Ensure your APP_ROOT points to the correct directory structure")
        sys.exit(1)

    # Check for protoc compiler
    try:
        subprocess.run(['protoc', '--version'], 
                      check=True, 
                      stdout=subprocess.PIPE,
                      stderr=subprocess.PIPE)
    except (subprocess.CalledProcessError, FileNotFoundError):
        print("Error: Protocol Buffers compiler (protoc) not found")
        print("Install with: sudo apt-get install protobuf-compiler")
        sys.exit(1)

    # Compile .proto file to generated directory
    try:
        print(f"Compiling {proto_file} to {generated_dir}...")
        subprocess.run([
            'protoc',
            f'--proto_path={os.path.dirname(proto_file)}',
            f'--python_out={generated_dir}',
            proto_file
        ], check=True, capture_output=True, text=True)
        print(f"Successfully generated core_pb2.py in {generated_dir}")
    except subprocess.CalledProcessError as e:
        print(f"protoc compilation failed: {e.stderr.strip()}")
        print("Full command attempted:")
        print(f"protoc --proto_path={os.path.dirname(proto_file)} "
              f"--python_out={generated_dir} {proto_file}")
        sys.exit(1)

    # Force reload if module was partially loaded
    if 'core_pb2' in sys.modules:
        del sys.modules['core_pb2']

    # Try importing again
    try:
        from gen import core_pb2
    except ImportError as e:
        print(f"Error: Failed to import core_pb2 after compilation: {e}")
        sys.exit(1)

def main():


    
    reply = core_pb2.LpmReply()
    data = bytes([0x08, 0x05])
    reply.ParseFromString(data)
    print(f"Received LpmReply: msg_id={reply.msg_id} response={reply.Response.Name(reply.response)}")

    if len(sys.argv) != 2:
        print("Usage: python send_core_message.py <serial_device>")
        print("Example: python send_core_message.py /dev/ttyXRUSB0")
        sys.exit(1)

    serial_device = sys.argv[1]

    try:
        # Configure serial port (adjust baudrate as needed)
        ser = serial.Serial(
            port=serial_device,
            baudrate=115200,
            timeout=1
        )
    except serial.SerialException as e:
        print(f"Error opening serial port: {e}")
        print("Check device permissions (typically requires dialout group membership)")
        sys.exit(1)

    print(f"Connected to {serial_device}. Sending CoreStatus and waiting for reply...")

    while True:
        try:
            # Create and send CoreMessage with CoreStatus
            core_msg = core_pb2.CoreMessage()
            core_msg.msg_id = random.randint(1, 1000000)

            status = core_pb2.CoreStatus()
            status.camera = core_pb2.CoreStatus.Status.OK
            status.controller = core_pb2.CoreStatus.Status.OK
            status.cloud_connection = core_pb2.CoreStatus.Status.OK
            status.storage_usage = 45.2

            core_msg.status.CopyFrom(status)

            serialized = core_msg.SerializeToString()
            ser.write(serialized)

            print(f"Sent CoreStatus msg_id={core_msg.msg_id} ({len(serialized)} bytes)")

            # Wait for reply
            reply_data = ser.read(1024)
            print(reply_data)
            if reply_data:
                reply = core_pb2.LpmReply()
                reply.ParseFromString(reply_data)
                print(f"Received LpmReply: msg_id={reply.msg_id} response={reply.Response.Name(reply.response)}")
            else:
                print("No reply received")

            time.sleep(10)

        except KeyboardInterrupt:
            print("\nStopping message transmission...")
            break
        except Exception as e:
            print(f"Transmission error: {e}")
            time.sleep(5)  # Brief recovery pause

if __name__ == "__main__":
    main()
