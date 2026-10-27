package mr.robot.agent.scrcpy.video;

import mr.robot.agent.scrcpy.control.PositionMapper;

public interface VirtualDisplayListener {
    void onNewVirtualDisplay(int displayId, PositionMapper positionMapper);
}
